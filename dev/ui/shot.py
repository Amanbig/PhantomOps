#!/usr/bin/env python3
"""Screenshot the PhantomOps UI headlessly, against a stand-in backend.

    python3 dev/ui/shot.py                       # every view, to /tmp/phantomops-shots
    python3 dev/ui/shot.py overview settings     # just these
    python3 dev/ui/shot.py settings --expand     # open every collapsible card
    python3 dev/ui/shot.py --size 900x700 --out /tmp/small
    python3 dev/ui/shot.py --clean               # remove the cached browser (~300 MB)

Views: overview incidents history activity connections add-connection settings dashboard
(connections = the Manage connections list; add-connection = the Add dialog;
dashboard = the Docker dashboard)

What it does: builds frontend/ (unless --no-build), serves the built frontend
on 127.0.0.1, injects a fake `window.go` / `window.runtime` (so no Go backend
and no Wails window are needed), and takes PNGs with headless Firefox. It never
opens a window on your screen and never talks to real infrastructure.

The first run downloads Playwright + Firefox into $PHANTOM_UI_HOME
(default /tmp/phantomops-ui). Needs python3 with venv, node/npm, Linux or macOS.

The fixture (connections, one incident, history, a Docker snapshot) is built
in; pass --fixture file.json to override any of: settings, last, history,
monitor, snapshots. Only the Docker dashboard has a snapshot fixture; other
dashboards show their "can't read" panel unless you supply one.
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import threading
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
HOME = Path(os.environ.get("PHANTOM_UI_HOME", "/tmp/phantomops-ui"))
VENV, BROWSERS = HOME / "venv", HOME / "browsers"
VIEWS = ["overview", "incidents", "history", "activity", "connections", "add-connection", "settings", "dashboard"]

# The fake backend. Times are relative to "now" so screenshots always look live.
STUB = r"""
(() => {
  const now = Date.now(), ago = m => new Date(now - m * 60000).toISOString();
  const OVERRIDE = __OVERRIDE__;
  const conns = [
    {id: 'docker1', type: 'docker', name: 'local-docker', enabled: true, socket: ''},
    {id: 'k8s1', type: 'kubernetes', name: 'k3s-home', enabled: true, context: '', kubeconfig: ''},
    {id: 'nomad1', type: 'nomad', name: 'nomad-dev', enabled: true, url: 'http://127.0.0.1:4646', token: '********'},
    {id: 'prom1', type: 'prometheus', name: 'prometheus', enabled: true, url: 'http://127.0.0.1:9090'},
    {id: 'loki1', type: 'loki', name: 'loki', enabled: true, url: 'http://127.0.0.1:3100', query: ''},
    {id: 'ssh1', type: 'ssh', name: 'edge-box', enabled: false, addr: 'edge.example:22', user: 'ops', keyPath: '', knownHosts: '', insecure: false, expectedPorts: [443]},
  ];
  const settings = {config: {environment: 'prod', windowMinutes: 30, ignoreSubjects: [], connections: conns,
    ai: {enabled: false, provider: 'ollama', baseUrl: 'http://localhost:11434/v1', model: '', apiKey: ''},
    remediation: {enabled: false}, monitoring: {enabled: true, intervalMinutes: 5, notify: true, notifyMinSeverity: 'critical'},
    updates: {autoCheckDisabled: false}, webhooks: [{id: 'h1', type: 'slack', name: '#ops-alerts', enabled: true, url: '********', minSeverity: 'critical'}]},
    path: '/home/you/phantomops-config.json', saved: true, notes: []};
  const incident = {id: 'a1b2c3', title: 'Deployment regression suspected: leaky-api', severity: 'critical', environment: 'PROD',
    detectedAt: ago(14), automatic: true,
    timeline: [
      {time: ago(18), source: 'kubernetes', kind: 'deployment', severity: 'info', title: 'Deployment rollout', detail: 'image leaky-api:v2', subject: 'default/leaky-api', origin: 'k3s-home'},
      {time: ago(14), source: 'docker', kind: 'resource', severity: 'warning', title: 'Memory climbing', detail: '78% of limit', subject: 'leaky-api', origin: 'local-docker'},
      {time: ago(11), source: 'docker', kind: 'resource', severity: 'critical', title: 'OOMKilled', detail: 'exit 137 (seen 3 times)', subject: 'leaky-api', origin: 'local-docker'},
      {time: ago(9), source: 'loki', kind: 'degradation', severity: 'warning', title: 'Upstream 5xx', detail: '502 from proxy', subject: 'proxy', origin: 'loki'}],
    investigation: {rootCause: 'The latest deployment of leaky-api appears to have introduced a memory problem: memory growth or OOM kills began 4m0s after it.',
      confidence: 0.85, recommendedAction: 'Roll back leaky-api to the previous stable deployment and investigate memory handling.',
      action: 'rollback', target: 'default/leaky-api', targetSource: 'kubernetes', targetConnection: 'k3s-home',
      reasoning: ['default/leaky-api was deployed 4 minutes before the first symptom.', 'Memory growth or OOM kills started after the deployment.', 'Symptoms occur on the same service that was deployed.'],
      source: 'rules', model: '', aiStatus: '', aiNote: '', citedEvents: [], ruleBasedRootCause: ''}};
  const last = {incidents: [incident], collectors: ['docker', 'kubernetes', 'nomad', 'prometheus', 'loki'], errors: {}, warnings: [], scannedAt: ago(1)};
  const record = {id: 'a1b2c3', key: 'PROD|x', title: incident.title, severity: 'critical', status: 'open', firstSeen: ago(14), lastSeen: ago(1),
    scans: 3, reopened: 0, latest: incident};
  const resolved = {...record, id: 'd4e5f6', title: 'Unhealthy service: cache', severity: 'warning', status: 'resolved', firstSeen: ago(300), lastSeen: ago(270), scans: 6,
    latest: {...incident, id: 'd4e5f6', title: 'Unhealthy service: cache', severity: 'warning', investigation: {...incident.investigation, rootCause: 'cache is running but failing its health checks.', action: ''}}};
  const c = (id, name, image, state, status, health, cpu, mem, lim, restarts, oom, exit) => ({id, name, image, state, status, health, exitCode: exit || 0, oomKilled: !!oom,
    restarts: restarts || 0, startedAt: ago(600), cpuPercent: cpu, memoryBytes: mem, memoryLimit: lim, ports: state === 'running' ? ['8080->80/tcp'] : [], compose: 'demo'});
  const MB = 1024 * 1024;
  const dockerSnap = {type: 'docker', takenAt: ago(0), engine: {version: '27.3.1', os: 'Ubuntu 24.04', kernel: '6.8.0', cpus: 8, memoryBytes: 16 * 1024 * MB, images: 14},
    containers: [c('1', 'api', 'demo/api:1.4', 'running', 'Up 3 hours', 'healthy', 12.4, 210 * MB, 512 * MB),
      c('2', 'db', 'postgres:16', 'running', 'Up 3 hours', 'healthy', 3.1, 180 * MB, null),
      c('3', 'leaky-api', 'demo/leaky-api:2', 'restarting', 'Restarting (137) 8 seconds ago', '', null, null, null, 7, true, 137),
      c('4', 'old-job', 'busybox', 'exited', 'Exited (1) 2 hours ago', '', null, null, null, 0, false, 1)],
    summary: {total: 4, running: 2, stopped: 1, unhealthy: 0, restarts: 7}};
  const D = {settings, last, history: [record, resolved], monitor: {enabled: true, intervalMinutes: 5, lastScan: ago(1), nextScan: ago(-4), scanning: false, notify: true, webhooks: []},
    snapshots: {docker: dockerSnap}, audit: [], deliveries: [], ...OVERRIDE};
  const App = {
    GetSettings: async () => JSON.parse(JSON.stringify(D.settings)), LastResult: async () => D.last, GetHistory: async () => D.history,
    GetMonitorStatus: async () => D.monitor, GetAppVersion: async () => '0.3.1', PendingUpdate: async () => null, MuteRules: async () => [],
    AuditLog: async () => D.audit, WebhookDeliveries: async () => D.deliveries,
    SaveSettings: async () => null, Scan: async () => D.last,
    ConnectionSnapshot: async id => { const t = (D.settings.config.connections.find(x => x.id === id) || {}).type;
      if (D.snapshots[t]) return D.snapshots[t]; throw 'no snapshot fixture for ' + t + ' (see dev/ui/shot.py --help)'; },
  };
  window.go = {main: {App: new Proxy(App, {get: (t, k) => k in t ? t[k] : (async () => null)})}};
  window.runtime = {EventsOn: () => () => {}, EventsOnMultiple: () => () => {}, EventsOff: () => {}, BrowserOpenURL: () => {},
    WindowIsMaximised: async () => false, Environment: async () => ({}), ClipboardSetText: async () => true};
})();
"""


def bootstrap(argv):
    """Create the venv + Firefox once, then re-run this script inside it."""
    py = VENV / "bin" / "python"
    if not py.exists():
        print("First run: installing Playwright (one time)…", file=sys.stderr)
        subprocess.check_call([sys.executable, "-m", "venv", str(VENV)])
        subprocess.check_call([str(py), "-m", "pip", "-q", "install", "playwright==1.49.0"])
    env = {**os.environ, "PLAYWRIGHT_BROWSERS_PATH": str(BROWSERS)}
    if not list(BROWSERS.glob("firefox-*")):
        print("First run: downloading headless Firefox (~100 MB)…", file=sys.stderr)
        subprocess.check_call([str(py), "-m", "playwright", "install", "firefox"], env=env)
    os.execve(str(py), [str(py), str(Path(__file__).resolve()), "--inner", *argv], env)


def serve(directory):
    class Quiet(SimpleHTTPRequestHandler):
        def log_message(self, *a):
            pass

    srv = ThreadingHTTPServer(("127.0.0.1", 0), partial(Quiet, directory=str(directory)))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("views", nargs="*", metavar="VIEW", help="default: all (" + ", ".join(VIEWS) + ")")
    ap.add_argument("--out", default="/tmp/phantomops-shots")
    ap.add_argument("--size", default="1100x800", help="WIDTHxHEIGHT (default 1100x800)")
    ap.add_argument("--expand", action="store_true", help="open every collapsible Settings card")
    ap.add_argument("--fixture", help="JSON file overriding settings/last/history/monitor/snapshots")
    ap.add_argument("--no-build", action="store_true", help="use the existing frontend build")
    ap.add_argument("--clean", action="store_true", help="delete the cached Playwright + Firefox and exit")
    ap.add_argument("--inner", action="store_true", help=argparse.SUPPRESS)
    args = ap.parse_args()

    if args.clean:
        shutil.rmtree(HOME, ignore_errors=True)
        print(f"removed {HOME}")
        return
    bad = [v for v in args.views if v not in VIEWS]
    if bad:
        sys.exit(f"unknown view(s): {', '.join(bad)} (choose from: {', '.join(VIEWS)})")
    try:
        w, h = (int(x) for x in args.size.lower().split("x"))
    except ValueError:
        sys.exit("--size must look like 1100x800")
    if not args.inner:
        bootstrap(sys.argv[1:])

    if not args.no_build:
        subprocess.check_call(["npm", "run", "build"], cwd=ROOT / "frontend", stdout=subprocess.DEVNULL)
        (ROOT / "frontend" / "package.json.md5").unlink(missing_ok=True)
    built = ROOT / "frontend" / "dist"
    if not (built / "index.html").exists():
        sys.exit("the frontend is not built; run without --no-build")

    override = json.loads(Path(args.fixture).read_text()) if args.fixture else {}
    stub = STUB.replace("__OVERRIDE__", json.dumps(override))
    views = args.views or VIEWS
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)

    from playwright.sync_api import sync_playwright

    srv = serve(built)
    errors = []
    try:
        with sync_playwright() as p:
            browser = p.firefox.launch(headless=True)
            page = browser.new_page(viewport={"width": w, "height": h})
            page.on("pageerror", lambda e: errors.append(str(e)))
            page.add_init_script(stub)
            page.goto(f"http://127.0.0.1:{srv.server_address[1]}/")
            page.wait_for_selector("#sidebar .sb-item", timeout=15000)
            for v in views:
                if v == "dashboard":
                    sel = "#sidebar .sb-conn[data-conn=docker1]"
                elif v == "add-connection":
                    sel = "#sidebar .sb-add"
                else:
                    sel = f"#sidebar .sb-item[data-view={v}]"
                page.click(sel)
                page.wait_for_timeout(700)
                if v == "settings" and args.expand:
                    for t in page.query_selector_all(".acc:not(.open) .acc-toggle"):
                        t.click()
                    page.wait_for_timeout(200)
                path = out / f"{v}.png"
                page.screenshot(path=str(path))
                print(path)
                if v == "add-connection":  # close the dialog so later views are clickable
                    page.click("#overlay", position={"x": 4, "y": 4})
                    page.wait_for_timeout(200)
            browser.close()
    finally:
        srv.shutdown()
    if errors:
        print("\nJavaScript errors while rendering:", *errors, sep="\n  ", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
