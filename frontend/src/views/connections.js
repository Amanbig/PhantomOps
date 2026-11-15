import {GetSettings, SaveSettings, TestConnection, InspectSSHHostKey, TrustSSHHostKey, DetectServices, TestAI, SendTestNotification, SendTestWebhook, GetMonitorStatus, LastResult, GetAppVersion, PendingUpdate} from '../../wailsjs/go/main/App';
import {esc, clock} from '../util.js';
import {logo} from '../logos.js';
import {mountUpdates} from './updates.js';

// Describes each connectable service and the form fields it needs.
const TYPES = {
    docker: {
        label: 'Docker / Podman',
        blurb: 'Container state, restarts, OOM kills, memory and error logs.',
        fields: [{key: 'socket', label: 'Local socket path', placeholder: '/var/run/docker.sock',
            help: 'Leave empty for the default. Podman works through its Docker-compatible socket.'},
            {key: 'sshConnection', label: 'Run through SSH connection', type: 'ssh-select',
                help: 'Optional. Select a saved SSH connection to run read-only Docker CLI checks on that host.'},
            {key: 'remoteSocket', label: 'Remote Docker socket', placeholder: '/var/run/docker.sock',
                help: 'Only used through SSH. Leave empty for Docker\'s default socket.'}],
    },
    kubernetes: {
        label: 'Kubernetes',
        blurb: 'Pods, events, rollouts and memory (works with k3s). Uses kubectl.',
        fields: [
            {key: 'context', label: 'kubectl context', placeholder: '(current context)',
                help: 'Leave empty to use the current context.'},
            {key: 'kubeconfig', label: 'kubeconfig file', placeholder: '(automatic)',
                help: 'Leave empty to use $KUBECONFIG, then ~/.kube/config. Set it if the app cannot find your cluster, e.g. when started from the desktop menu.'},
            {key: 'sshConnection', label: 'Run through SSH connection', type: 'ssh-select',
                help: 'Optional. For a cluster on another server: run read-only kubectl checks on that host over a saved SSH connection. Monitoring only; rollbacks are not performed through SSH, and the local kubeconfig above is not used.'},
            {key: 'remoteKubeconfig', label: 'Remote kubeconfig file', placeholder: '/home/user/.kube/config',
                help: 'Only used through SSH. Absolute path on that host (~ is not expanded). Leave empty to use kubectl\'s own lookup there. Non-interactive SSH does not read ~/.bashrc, so set this if kubectl cannot find the cluster.'},
        ],
    },
    prometheus: {
        label: 'Prometheus',
        blurb: 'Metric anomalies: memory growth, targets down, firing alerts, 5xx rates.',
        fields: [
            {key: 'url', label: 'URL', placeholder: 'http://localhost:9090', required: true},
            {key: 'sshConnection', label: 'Run through SSH connection', type: 'ssh-select',
            help: 'Optional. For a service on another server: connect through a saved SSH connection (read-only; nothing opens on this computer). The URL is then the address as seen from that server, e.g. http://127.0.0.1:9090. Needs port forwarding allowed on the SSH server.'},
        ],
    },
    loki: {
        label: 'Loki',
        blurb: 'Error-looking log lines grouped per service.',
        fields: [
            {key: 'url', label: 'URL', placeholder: 'http://localhost:3100', required: true},
            {key: 'query', label: 'LogQL query', placeholder: '(default: error lines from all services)'},
            {key: 'sshConnection', label: 'Run through SSH connection', type: 'ssh-select',
            help: 'Optional. For a service on another server: connect through a saved SSH connection (read-only; nothing opens on this computer). The URL is then the address as seen from that server, e.g. http://127.0.0.1:3100. Needs port forwarding allowed on the SSH server.'},
        ],
    },
    nomad: {
        label: 'Nomad',
        blurb: 'Job and allocation status, task failures and OOM kills.',
        fields: [
            {key: 'url', label: 'URL', placeholder: 'http://localhost:4646', required: true},
            {key: 'token', label: 'ACL token', type: 'password', placeholder: '(optional)',
                help: 'Stored locally in the config file (owner-only permissions).'},
            {key: 'sshConnection', label: 'Run through SSH connection', type: 'ssh-select',
            help: 'Optional. For a service on another server: connect through a saved SSH connection (read-only; nothing opens on this computer). The URL is then the address as seen from that server, e.g. http://127.0.0.1:4646. Needs port forwarding allowed on the SSH server.'},
        ],
    },
    ssh: {
        label: 'SSH host',
        blurb: 'Read-only diagnostics: load, memory, disk, failed units, OOM, open ports.',
        fields: [
            {key: 'addr', label: 'Host', placeholder: 'web-01.example.com:22', required: true},
            {key: 'user', label: 'User', placeholder: 'ops', required: true},
            {key: 'keyPath', label: 'Private key path', placeholder: '(empty = use ssh-agent)',
                help: 'Unencrypted key file. Passphrase-protected keys: use ssh-agent.'},
            {key: 'knownHosts', label: 'known_hosts file', placeholder: '~/.ssh/known_hosts'},
            {key: 'expectedPorts', label: 'Expected open ports', placeholder: '80, 443',
                help: 'Comma-separated TCP ports that should be listening.'},
            {key: 'insecure', label: 'Skip host key verification', type: 'checkbox',
                help: 'Insecure. Only for testing.'},
        ],
    },
};

const DEFAULT_COUNT_HINT = 'Nothing is connected yet. Add a service, or use “Detect local services” to find Docker and kubectl on this computer.';

const AI_PRESETS = {
    ollama: {label: 'Ollama (local)', url: 'http://localhost:11434/v1', keyHelp: 'Not needed for a local Ollama.'},
    openai: {label: 'OpenAI-compatible API', url: 'https://api.openai.com/v1', keyHelp: 'Sent as a Bearer token.'},
};

function isLocalUrl(u) {
    try {
        const h = new URL(u).hostname;
        return h === 'localhost' || h.endsWith('.local') || h === '::1' || h === '[::1]' ||
            /^127\./.test(h) || /^10\./.test(h) || /^192\.168\./.test(h) || /^172\.(1[6-9]|2\d|3[01])\./.test(h);
    } catch { return false; }
}

let state = {hookDraft: null, hookStatus: {}, settings: null, detected: null, health: {}, form: null, error: '', aiTest: null, aiModels: [], mode: 'connections', focusId: '', scanHealth: {},
    open: new Set(['ai']), appVersion: '', pendingUpdate: null};
let root;
let onChange = () => {};

// typeInfo returns the label and icon of a connection type (used by the sidebar).
export function typeInfo(type) {
    return TYPES[type] || {label: type};
}

// onConnectionsChanged registers a callback run after settings are saved.
export function onConnectionsChanged(cb) { onChange = cb; }

// mountConnections shows the connection list (focusId highlights one card).
export async function mountConnections(el, {focusId = '', add = false} = {}) {
    state.mode = 'connections';
    state.focusId = focusId;
    state.form = add ? {type: '', conn: null} : null;
    root = el;
    root.innerHTML = '<p class="muted">Loading…</p>';
    await refresh();
}

async function refresh() {
    try {
        state.settings = await GetSettings();
        state.error = '';
        state.scanHealth = scanHealth(await LastResult().catch(() => null));
        state.appVersion = await GetAppVersion().catch(() => state.appVersion);
        state.pendingUpdate = await PendingUpdate().catch(() => null);
        const mon = await GetMonitorStatus().catch(() => null);
        state.hookStatus = Object.fromEntries((mon?.webhooks || []).map(w => [w.id, w]));
        state.hookDraft = null;
    } catch (err) {
        state.error = String(err);
    }
    render();
}

async function persist(cfg, okMessage) {
    try {
        await SaveSettings(cfg);
        state.error = '';
        await refresh();
        onChange();
        if (okMessage) toast(okMessage);
        return true;
    } catch (err) {
        state.error = String(err);
        render();
        return false;
    }
}

function toast(msg) {
    document.querySelectorAll('.toast').forEach(t => t.remove());
    const t = document.createElement('div');
    t.className = 'toast';
    t.textContent = msg;
    document.body.appendChild(t);
    setTimeout(() => t.remove(), 2500);
}

function cloneConfig() {
    return JSON.parse(JSON.stringify(state.settings.config));
}

function summary(c) {
    switch (c.type) {
        case 'docker': return c.sshConnection ? `via SSH · ${c.remoteSocket || '/var/run/docker.sock'}` : (c.socket || '/var/run/docker.sock');
        case 'kubernetes': return c.sshConnection ? `via SSH · ${c.context ? 'context: ' + c.context : 'current kubectl context'}${c.remoteKubeconfig ? ' · ' + c.remoteKubeconfig : ''}`
            : (c.context ? `context: ${c.context}` : 'current kubectl context') + (c.kubeconfig ? ` · ${c.kubeconfig}` : '');
        case 'ssh': return `${c.user}@${c.addr}` + (c.expectedPorts?.length ? ` · ports ${c.expectedPorts.join(', ')}` : '');
        default: return (c.sshConnection ? 'via SSH · ' : '') + c.url;
    }
}

function sameTarget(a, b) {
    return a.type === b.type && (a.socket || '') === (b.socket || '') && (a.sshConnection || '') === (b.sshConnection || '') && (a.remoteSocket || '') === (b.remoteSocket || '') && (a.remoteKubeconfig || '') === (b.remoteKubeconfig || '') && (a.context || '') === (b.context || '') &&
        (a.url || '') === (b.url || '') && (a.addr || '') === (b.addr || '') && (a.kubeconfig || '') === (b.kubeconfig || '');
}

// ---------- rendering ----------

// scanHealth maps connection name -> {ok, message} from a scan result.
export function scanHealth(res) {
    const out = {};
    if (!res || !res.scannedAt || String(res.scannedAt).startsWith('0001')) return out;
    for (const name of res.collectors || []) {
        const err = (res.errors || {})[name];
        out[name] = err ? {ok: false, message: err} : {ok: true, message: 'Healthy in the last scan'};
    }
    return out;
}

function render() {
    if (!state.settings) {
        root.innerHTML = `<p class="error">${esc(state.error || 'Could not load settings.')}</p>`;
        return;
    }
    const {config, path, saved, notes} = state.settings;
    const conns = config.connections || [];

    if (state.mode === 'settings') {
        root.innerHTML = `
            <div class="toolbar"><h2 class="view-title">Settings</h2></div>
            ${state.error ? `<div class="alert alert-bad">${esc(state.error)}</div>` : ''}
            ${renderAI(config.ai || {})}
            ${renderMonitoring(config.monitoring || {})}
            ${renderWebhooks(state.hookDraft || config.webhooks || [])}
            ${renderRemediation(config.remediation || {})}
            ${renderGeneral(config)}
            ${renderUpdates()}
            ${renderSupport()}
            <p class="muted small">Config file: <code>${esc(path)}</code></p>`;
        bind();
        return;
    }

    root.innerHTML = `
        <div class="toolbar"><h2 class="view-title">Connections</h2>
            <div class="toolbar-actions">
                <button class="btn btn-secondary" id="detect">Detect local services</button>
                <button class="btn" id="add">+ Add connection</button>
            </div></div>
        ${state.error ? `<div class="alert alert-bad">${esc(state.error)}</div>` : ''}
        ${(notes || []).map(n => `<div class="alert alert-bad">${esc(n)}</div>`).join('')}
        ${state.detected ? renderDetected(conns) : ''}
        <div class="conn-list">
            ${conns.length ? conns.map(renderCard).join('') : `<div class="empty">
                <div class="empty-icon">🔌</div><h3>No connections yet</h3>
                <p class="muted">${DEFAULT_COUNT_HINT}</p></div>`}
        </div>
        <p class="muted small">AI, monitoring, remediation and update settings are under <a href="#" id="goto-settings">Settings</a>.</p>
        ${state.form ? renderForm() : ''}`;
    if (state.focusId) {
        const card = root.querySelector(`.conn-card[data-id="${CSS.escape(state.focusId)}"]`);
        if (card) { card.classList.add('conn-focus'); card.scrollIntoView({block: 'center'}); }
        state.focusId = '';
    }
    bind();
}

function renderCard(c) {
    const t = TYPES[c.type] || {label: c.type};
    const h = state.health[c.id] || (c.enabled ? state.scanHealth[c.name] : null);
    const dot = h ? (h.ok ? 'dot-ok' : 'dot-bad') : 'dot-idle';
    return `
        <div class="conn-card ${c.enabled ? '' : 'conn-off'}" data-id="${esc(c.id)}">
            <div class="conn-icon">${logo(c.type, 28)}</div>
            <div class="conn-main">
                <div class="conn-name"><span class="dot ${dot}"></span>${esc(c.name)}
                    <span class="chip">${esc(t.label)}</span></div>
                <div class="conn-sub">${esc(summary(c))}</div>
                ${h ? `<div class="conn-health ${h.ok ? 'ok' : 'bad'}">${esc(h.message)}</div>` : ''}
            </div>
            <div class="conn-actions">
                <label class="switch" title="Enable or disable"><input type="checkbox" data-action="toggle" ${c.enabled ? 'checked' : ''}><span></span></label>
                <button class="btn btn-small btn-secondary" data-action="test">Test</button>
                <button class="btn btn-small btn-secondary" data-action="edit">Edit</button>
                <button class="btn btn-small btn-danger" data-action="delete">Delete</button>
            </div>
        </div>`;
}

function renderDetected(existing) {
    const fresh = state.detected.filter(d => !existing.some(e => sameTarget(e, d)));
    return `<div class="panel">
        <div class="panel-head"><strong>Found on this machine</strong>
            <button class="btn btn-small btn-secondary" id="close-detected">Close</button></div>
        ${fresh.length ? fresh.map((d, i) => {
            const t = TYPES[d.type];
            return `<div class="detected-row"><span class="detected-name">${logo(d.type, 18)} ${esc(d.name)} <span class="muted small">${esc(summary(d))}</span></span>
                <button class="btn btn-small" data-add-detected="${i}">Add</button></div>`;
        }).join('') + (fresh.length > 1 ? '<button class="btn btn-small" id="add-all">Add all</button>' : '')
        : '<p class="muted">Nothing new found. Everything detected is already connected.</p>'}
        </div>`;
}

// acc renders a collapsible settings card. The header toggles it; headExtra
// (e.g. an "Enabled" switch) sits next to the toggle so it works while collapsed.
function acc(id, title, summaryText, headExtra, body, panelId = '') {
    const open = state.open.has(id);
    return `<section class="panel acc ${open ? 'open' : ''}" data-acc="${id}"${panelId ? ` id="${panelId}"` : ''}>
        <div class="acc-head">
            <button type="button" class="acc-toggle" data-acc-toggle="${id}" aria-expanded="${open}" aria-controls="acc-body-${id}">
                <span class="acc-chevron" aria-hidden="true"></span>
                <strong>${title}</strong>
                <span class="acc-summary">${esc(summaryText)}</span>
            </button>${headExtra || ''}
        </div>
        <div class="acc-body" id="acc-body-${id}"${open ? '' : ' hidden'}>${body}</div>
    </section>`;
}

function renderAI(ai) {
    const provider = ai.provider || 'ollama';
    const url = ai.baseUrl || AI_PRESETS[provider].url;
    const local = isLocalUrl(url);
    const summaryText = ai.enabled ? `On · ${ai.model || 'no model set'} · ${local ? 'local' : 'remote'}` : 'Off';
    const body = `
        <p class="muted small" style="margin-top:0">A language model double-checks Phantom's rule-based diagnosis. Its answer must cite real timeline events and may only mention services that appear in your data. It can never trigger actions on its own. Off by default.</p>
        <div class="form-grid">
            <label>Provider<select id="ai-provider">${Object.entries(AI_PRESETS).map(([k, p]) =>
                `<option value="${k}" ${k === provider ? 'selected' : ''}>${esc(p.label)}</option>`).join('')}</select></label>
            <label>Model<input id="ai-model" list="ai-models" value="${esc(ai.model || '')}" placeholder="llama3.1:8b">
                <datalist id="ai-models">${state.aiModels.map(m => `<option value="${esc(m)}">`).join('')}</datalist>
                <span class="muted small">Models under ~3B parameters are fast but often give weaker answers than the built-in rules; 7B+ is recommended.</span></label>
            <label class="span2">Base URL<input id="ai-url" value="${esc(url)}" placeholder="${esc(AI_PRESETS[provider].url)}"></label>
            <label class="span2">API key<input id="ai-key" type="password" value="${esc(ai.apiKey || '')}" placeholder="(optional)">
                <span class="muted small">${esc(AI_PRESETS[provider].keyHelp)} Stored locally in the config file (owner-only permissions).</span></label>
        </div>
        <div class="privacy ${local ? 'privacy-local' : 'privacy-remote'}" id="ai-privacy">
            ${local ? '✅ Local endpoint: incident data stays on your machine or network.'
                    : '⚠ Remote endpoint: incident timelines (service names, event details and log excerpts) will be sent to this server.'}</div>
        ${state.aiTest ? `<div class="alert ${state.aiTest.ok ? 'alert-ok' : 'alert-bad'}">${esc(state.aiTest.message)}</div>` : ''}
        <div class="toolbar-actions">
            <button class="btn btn-small btn-secondary" id="ai-test">Test &amp; load models</button>
            <button class="btn btn-small" id="ai-save">Save AI settings</button>
        </div>`;
    return acc('ai', '🤖 AI investigator', summaryText,
        `<label class="check acc-switch"><input type="checkbox" id="ai-enabled" ${ai.enabled ? 'checked' : ''}> Enabled</label>`, body, 'ai-panel');
}

function renderMonitoring(m) {
    const sev = m.notifyMinSeverity || 'critical';
    const summaryText = m.enabled ? `Every ${m.intervalMinutes || 5} min${m.notify ? ' · notifications on' : ''}` : 'Off';
    const body = `
        <p class="muted small" style="margin:0 0 10px">While PhantomOps is open, it scans on a schedule, keeps the incident history up to date, and can send a desktop notification when something new breaks. Manual scans never notify.</p>
        <div class="form-grid">
            <label>Scan every (minutes)<input id="mon-interval" type="number" min="1" max="1440" value="${esc(m.intervalMinutes || 5)}"></label>
            <label>Notify for<select id="mon-sev">
                <option value="critical" ${sev === 'critical' ? 'selected' : ''}>Critical incidents</option>
                <option value="warning" ${sev === 'warning' ? 'selected' : ''}>Warnings and critical</option></select></label>
        </div>
        <label class="check"><input type="checkbox" id="mon-notify" ${m.notify ? 'checked' : ''}> Desktop notifications (new, escalated, returning and resolved incidents)</label>
        <div id="mon-msg"></div>
        <div class="toolbar-actions">
            <button class="btn btn-small btn-secondary" id="mon-test">Send test notification</button>
            <button class="btn btn-small" id="mon-save">Save monitoring</button>
        </div>`;
    return acc('monitoring', '⏱ Monitoring', summaryText,
        `<label class="check acc-switch"><input type="checkbox" id="mon-enabled" ${m.enabled ? 'checked' : ''}> Scan in the background</label>`, body, 'mon-panel');
}

const HOOK_TYPES = {slack: 'Slack', discord: 'Discord', generic: 'Generic JSON'};
const HOOK_HINTS = {slack: 'https://hooks.slack.com/services/…', discord: 'https://discord.com/api/webhooks/…', generic: 'https://example.com/hook'};

function renderWebhooks(list) {
    const active = list.filter(w => w.enabled).length;
    const summaryText = list.length ? `${list.length} channel${list.length > 1 ? 's' : ''}${active ? ` · ${active} on` : ' · all off'}` : 'None';
    const rows = list.map((w, i) => {
        const st = state.hookStatus[w.id];
        const status = !st ? '' : st.ok
            ? `<span class="muted small">Last delivery OK · ${esc(clock(st.at))}</span>`
            : `<span class="small" style="color:var(--bad, #f87171)">Last delivery failed: ${esc(st.error || 'unknown error')}</span>`;
        const sev = w.minSeverity || 'critical';
        return `<div class="hook-row" data-i="${i}" data-id="${esc(w.id || '')}">
            <div class="form-grid">
                <label>Name<input data-f="name" value="${esc(w.name || '')}" placeholder="#ops-alerts"></label>
                <label>Type<select data-f="type">${Object.entries(HOOK_TYPES).map(([k, v]) => `<option value="${k}" ${w.type === k ? 'selected' : ''}>${v}</option>`).join('')}</select></label>
                <label>Webhook URL<input data-f="url" type="password" autocomplete="off" value="${esc(w.url || '')}" placeholder="${esc(HOOK_HINTS[w.type] || HOOK_HINTS.slack)}"></label>
                <label>Send for<select data-f="minSeverity">
                    <option value="critical" ${sev === 'critical' ? 'selected' : ''}>Critical incidents</option>
                    <option value="warning" ${sev === 'warning' ? 'selected' : ''}>Warnings and critical</option></select></label>
            </div>
            <label class="check"><input type="checkbox" data-f="enabled" ${w.enabled ? 'checked' : ''}> Enabled</label>
            <div class="toolbar-actions">
                <button class="btn btn-small btn-secondary" data-hook-test="${i}">Send test message</button>
                <button class="btn btn-small btn-secondary" data-hook-del="${i}">Remove</button>
            </div>
            <div class="hook-msg" data-hook-msg="${i}">${status}</div>
        </div>`;
    }).join('');
    const body = `
        <p class="muted small" style="margin:0 0 10px">Post incident updates to Slack, Discord or any JSON endpoint while PhantomOps monitors in the background. Nothing is sent unless you add a channel. Messages contain only the title, severity and root-cause summary (never logs or credentials), and muted or snoozed incidents stay silent. The URL is stored in the config file (owner-only) and hidden here.</p>
        ${rows || '<p class="muted small">No channels yet.</p>'}
        <div id="hook-err"></div>
        <div class="toolbar-actions">
            <button class="btn btn-small btn-secondary" id="hook-add">Add channel</button>
            <button class="btn btn-small" id="hook-save">Save channels</button>
        </div>`;
    return acc('webhooks', '💬 Chat &amp; webhooks', summaryText, '', body, 'hooks-panel');
}

// readHooks collects the channel rows from the form.
function readHooks() {
    return [...root.querySelectorAll('.hook-row')].map(row => {
        const f = n => row.querySelector(`[data-f="${n}"]`);
        return {id: row.dataset.id, name: f('name').value.trim(), type: f('type').value, url: f('url').value.trim(),
            minSeverity: f('minSeverity').value, enabled: f('enabled').checked};
    });
}

function renderRemediation(rem) {
    const body = `
        <p class="muted small" style="margin:0 0 8px">When on, an incident that points at a bad Kubernetes or Nomad deployment can be rolled back to its previous revision. Every rollback shows a preview of exactly what changes, needs you to type the workload name, and is recorded on the Activity tab. Never runs automatically. Off by default.</p>
        <button class="btn btn-small" id="rem-save">Save</button>`;
    return acc('remediation', '🛠 Remediation', rem.enabled ? 'Rollbacks allowed (with confirmation)' : 'Off',
        `<label class="check acc-switch"><input type="checkbox" id="rem-enabled" ${rem.enabled ? 'checked' : ''}> Allow rollbacks</label>`, body, 'rem-panel');
}

function renderGeneral(config) {
    const ignored = (config.ignoreSubjects || []).length;
    const summaryText = `${config.environment || 'no environment label'} · ${config.windowMinutes || 30} min window${ignored ? ` · ${ignored} ignored` : ''}`;
    const body = `
        <div class="form-grid">
            <label>Environment label<input id="g-env" value="${esc(config.environment)}" placeholder="production"></label>
            <label>Look-back window (minutes)<input id="g-window" type="number" min="1" max="1440" value="${esc(config.windowMinutes || 30)}"></label>
            <label class="span2">Ignore subjects (comma-separated substrings)
                <input id="g-ignore" value="${esc((config.ignoreSubjects || []).join(', '))}" placeholder="monitoring, cadvisor"></label>
        </div>
        <button class="btn btn-small" id="save-general">Save settings</button>`;
    return acc('analysis', '🔎 Analysis settings', summaryText, '', body, 'analysis-panel');
}

// The Updates card's contents are drawn by views/updates.js into #updates-panel.
function renderUpdates() {
    const v = state.appVersion === 'dev' ? 'development build' : state.appVersion;
    const pend = state.pendingUpdate && state.pendingUpdate.available ? ` · ${state.pendingUpdate.latestVersion} available` : '';
    return acc('updates', '🔄 About &amp; updates', `${v ? 'PhantomOps ' + v : 'PhantomOps'}${pend}`, '', '<div id="updates-panel"></div>', 'updates-card');
}

function renderForm() {
    const f = state.form;
    if (!f.type) {
        return `<div class="overlay" id="overlay"><div class="modal">
            <div class="modal-head"><h3>Add connection</h3><button class="btn btn-small btn-secondary" data-close>✕</button></div>
            <div class="type-grid">${Object.entries(TYPES).map(([k, t]) => `
                <button class="type-card" data-type="${k}"><div class="type-icon">${logo(k, 30)}</div>
                <strong>${esc(t.label)}</strong><span class="muted small">${esc(t.blurb)}</span></button>`).join('')}
            </div></div></div>`;
    }
    const t = TYPES[f.type];
    const c = f.conn;
    return `<div class="overlay" id="overlay"><div class="modal">
        <div class="modal-head"><h3 class="modal-title">${logo(f.type, 22)} ${f.editing ? 'Edit' : 'Add'} ${esc(t.label)}</h3>
            <button class="btn btn-small btn-secondary" data-close>✕</button></div>
        <form id="conn-form" autocomplete="off">
            <label>Name<input name="name" required value="${esc(c.name)}"></label>
            ${t.fields.map(fd => {
                const v = fd.key === 'expectedPorts' ? (c.expectedPorts || []).join(', ') : (c[fd.key] ?? '');
                if (fd.type === 'checkbox') {
                    return `<label class="check"><input type="checkbox" name="${fd.key}" ${c[fd.key] ? 'checked' : ''}> ${esc(fd.label)}
                        ${fd.help ? `<span class="muted small">${esc(fd.help)}</span>` : ''}</label>`;
                }
                if (fd.type === 'ssh-select') {
                    const ssh = (state.settings?.config?.connections || []).filter(x => x.type === 'ssh');
                    return `<label>${esc(fd.label)}<select name="${esc(fd.key)}"><option value="">Directly from this computer</option>${ssh.map(x => `<option value="${esc(x.id)}" ${c[fd.key] === x.id ? 'selected' : ''}>${esc(x.name)} · ${esc(x.user || '')}@${esc(x.addr || '')}</option>`).join('')}</select>
                        ${fd.help ? `<span class="muted small">${esc(fd.help)}</span>` : ''}</label>`;
                }
                return `<label>${esc(fd.label)}${fd.required ? ' *' : ''}
                    <input name="${fd.key}" type="${fd.type || 'text'}" value="${esc(v)}" placeholder="${esc(fd.placeholder || '')}" ${fd.required ? 'required' : ''}>
                    ${fd.help ? `<span class="muted small">${esc(fd.help)}</span>` : ''}</label>`;
            }).join('')}
            <label class="check"><input type="checkbox" name="enabled" ${c.enabled ? 'checked' : ''}> Enabled</label>
            ${f.type === 'ssh' ? renderHostKeyBox() : ''}
            <div id="form-test" class="form-test"></div>
            ${f.error ? `<div class="alert alert-bad">${esc(f.error)}</div>` : ''}
            <div class="modal-actions">
                <button type="button" class="btn btn-secondary" id="form-test-btn">Test connection</button>
                <button type="submit" class="btn">${f.editing ? 'Save changes' : 'Add connection'}</button>
            </div>
        </form></div></div>`;
}

// The host-key panel on the SSH form. Its contents are drawn into #hostkey-out
// by the handlers below, so checking a host never re-renders (and never
// clears) the rest of the form.
function renderHostKeyBox() {
    return `<div class="hostkey-box">
        <div class="hostkey-head"><strong>Host key</strong>
            <button type="button" class="btn btn-small btn-secondary" id="hostkey-check">Check host key</button></div>
        <p class="muted small" style="margin:6px 0 0">Reads the server's key without logging in. If the server is new to this computer you can compare its fingerprint and trust it, instead of editing known_hosts by hand.</p>
        <div id="hostkey-out"></div>
    </div>`;
}

// The file on the server that holds each key type, for the comparison advice.
// A server usually has several host keys; the one shown is the one to compare.
const HOSTKEY_FILE = {'ssh-ed25519': 'ed25519', 'ecdsa-sha2-nistp256': 'ecdsa', 'ecdsa-sha2-nistp384': 'ecdsa',
    'ecdsa-sha2-nistp521': 'ecdsa', 'ssh-rsa': 'rsa', 'ssh-dss': 'dsa'};

// drawHostKey shows the result of a host-key check in the panel.
function drawHostKey(out, info) {
    const fp = `<div class="hostkey-fp"><span class="muted small">${esc(info.keyType)} · SHA256 fingerprint</span><code>${esc(info.fingerprint)}</code></div>`;
    if (info.status === 'trusted') {
        out.innerHTML = `<div class="alert alert-ok"><span class="alert-text">Trusted. This server's key is already in <code>${esc(info.knownHostsPath)}</code>.</span></div>${fp}`;
    } else if (info.status === 'changed') {
        out.innerHTML = `<div class="alert alert-bad"><span class="alert-text"><strong>The host key has changed.</strong> known_hosts has a different key for this server
            (${(info.known || []).map(k => `<code>${esc(k)}</code>`).join(', ')}). This can mean the server was reinstalled, or that someone is intercepting the connection.
            PhantomOps will not connect and will not replace the old key. Verify the new fingerprint with the server's owner, then remove the old line from
            <code>${esc(info.knownHostsPath)}</code> yourself.</span></div>${fp}`;
    } else {
        out.innerHTML = `<div class="alert"><span class="alert-text">This server is not in <code>${esc(info.knownHostsPath)}</code> yet. Compare this fingerprint with one you got from a trusted source
            ${HOSTKEY_FILE[info.keyType]
                ? `(for example by running <code>ssh-keygen -lf /etc/ssh/ssh_host_${HOSTKEY_FILE[info.keyType]}_key.pub</code> on the server; a server can have several keys, so compare the ${esc(HOSTKEY_FILE[info.keyType])} one)`
                : '(for example from the server\'s own <code>/etc/ssh/ssh_host_*_key.pub</code> files)'}
            before trusting it.</span></div>${fp}
            <div class="toolbar-actions"><button type="button" class="btn btn-small" id="hostkey-trust" data-fp="${esc(info.fingerprint)}">Trust this key</button></div>`;
    }
}

// ---------- behaviour ----------

function readForm() {
    const form = root.querySelector('#conn-form');
    const data = new FormData(form);
    const c = {...state.form.conn};
    c.name = (data.get('name') || '').trim();
    c.enabled = form.elements.enabled.checked;
    for (const fd of TYPES[state.form.type].fields) {
        if (fd.type === 'checkbox') { c[fd.key] = form.elements[fd.key].checked; continue; }
        const raw = (data.get(fd.key) || '').toString().trim();
        if (fd.key === 'expectedPorts') {
            c.expectedPorts = raw ? raw.split(',').map(s => parseInt(s.trim(), 10)) : [];
        } else {
            c[fd.key] = raw;
        }
    }
    return c;
}

function blankConn(type) {
    const t = TYPES[type];
    return {id: '', type, name: t.label, enabled: true, socket: '', sshConnection: '', remoteSocket: '', remoteKubeconfig: '', context: '', kubeconfig: '', url: '', query: '', token: '',
        addr: '', user: '', keyPath: '', knownHosts: '', insecure: false, expectedPorts: []};
}

function bind() {
    const $ = s => root.querySelector(s);
    // Settings accordion: toggle in place so unsaved edits in other cards survive.
    root.querySelectorAll('[data-acc-toggle]').forEach(btn => btn.addEventListener('click', () => {
        const id = btn.dataset.accToggle, card = btn.closest('.acc'), body = card.querySelector('.acc-body');
        const open = !state.open.has(id);
        if (open) state.open.add(id); else state.open.delete(id);
        card.classList.toggle('open', open);
        btn.setAttribute('aria-expanded', String(open));
        body.hidden = !open;
    }));
    $('#goto-settings')?.addEventListener('click', e => { e.preventDefault(); gotoSettings(); });
    const up = $('#updates-panel');
    if (up) mountUpdates(up);

    $('#add')?.addEventListener('click', () => { state.form = {type: '', conn: null}; render(); });
    $('#detect')?.addEventListener('click', async e => {
        e.target.disabled = true; e.target.textContent = 'Detecting…';
        try { state.detected = await DetectServices(); } catch (err) { state.error = String(err); }
        render();
    });
    $('#close-detected')?.addEventListener('click', () => { state.detected = null; render(); });

    root.querySelectorAll('[data-add-detected]').forEach(b => b.addEventListener('click', () => {
        const fresh = state.detected.filter(d => !state.settings.config.connections.some(e => sameTarget(e, d)));
        const cfg = cloneConfig();
        cfg.connections.push(fresh[Number(b.dataset.addDetected)]);
        persist(cfg, 'Connection added');
    }));
    $('#add-all')?.addEventListener('click', () => {
        const cfg = cloneConfig();
        cfg.connections.push(...state.detected.filter(d => !cfg.connections.some(e => sameTarget(e, d))));
        persist(cfg, 'Connections added');
    });

    root.querySelectorAll('.conn-card').forEach(card => {
        const id = card.dataset.id;
        const find = () => state.settings.config.connections.find(c => c.id === id);
        card.querySelector('[data-action=toggle]').addEventListener('change', e => {
            const cfg = cloneConfig();
            cfg.connections.find(c => c.id === id).enabled = e.target.checked;
            persist(cfg);
        });
        card.querySelector('[data-action=test]').addEventListener('click', async e => {
            e.target.disabled = true; e.target.textContent = 'Testing…';
            state.health[id] = await TestConnection(find());
            render();
        });
        card.querySelector('[data-action=edit]').addEventListener('click', () => {
            const c = find();
            state.form = {type: c.type, conn: {...c}, editing: true, error: ''};
            render();
        });
        card.querySelector('[data-action=delete]').addEventListener('click', () => {
            const c = find();
            if (!confirm(`Delete connection "${c.name}"?`)) return;
            const cfg = cloneConfig();
            cfg.connections = cfg.connections.filter(x => x.id !== id);
            delete state.health[id];
            persist(cfg, 'Connection deleted');
        });
    });

    // AI panel
    const readAI = () => ({
        enabled: $('#ai-enabled').checked, provider: $('#ai-provider').value, baseUrl: $('#ai-url').value.trim(),
        model: $('#ai-model').value.trim(), apiKey: $('#ai-key').value,
    });
    $('#ai-provider')?.addEventListener('change', e => {
        const urlEl = $('#ai-url');
        const known = Object.values(AI_PRESETS).map(p => p.url);
        if (!urlEl.value.trim() || known.includes(urlEl.value.trim())) urlEl.value = AI_PRESETS[e.target.value].url;
        state.aiTest = null;
        const cfg = cloneConfig(); cfg.ai = {...readAI()}; state.settings.config = cfg;
        render();
    });
    $('#ai-url')?.addEventListener('input', e => {
        const local = isLocalUrl(e.target.value);
        const el = $('#ai-privacy');
        el.className = 'privacy ' + (local ? 'privacy-local' : 'privacy-remote');
        el.textContent = local ? '✅ Local endpoint: incident data stays on your machine or network.'
            : '⚠ Remote endpoint: incident timelines (service names, event details and log excerpts) will be sent to this server.';
    });
    $('#ai-test')?.addEventListener('click', async e => {
        e.target.disabled = true; e.target.textContent = 'Testing…';
        const res = await TestAI(readAI());
        state.aiTest = res;
        state.aiModels = res.models || [];
        const cfg = cloneConfig(); cfg.ai = {...readAI()}; state.settings.config = cfg;
        render();
    });
    $('#ai-save')?.addEventListener('click', async () => {
        const ai = readAI();
        if (ai.enabled && !isLocalUrl(ai.baseUrl) &&
            !confirm(`Incident data (service names, event details, log excerpts) will be sent to ${new URL(ai.baseUrl).host}. Continue?`)) return;
        const cfg = cloneConfig();
        cfg.ai = ai;
        state.aiTest = null;
        persist(cfg, 'AI settings saved');
    });

    $('#mon-test')?.addEventListener('click', async () => {
        const msg = $('#mon-msg');
        try { await SendTestNotification(); msg.innerHTML = '<div class="alert alert-ok">Test notification sent. If nothing appeared, check that your desktop allows notifications.</div>'; }
        catch (err) { msg.innerHTML = `<div class="alert alert-bad">${esc(String(err))}</div>`; }
    });
    $('#mon-save')?.addEventListener('click', () => {
        const cfg = cloneConfig();
        cfg.monitoring = {
            enabled: $('#mon-enabled').checked,
            intervalMinutes: parseInt($('#mon-interval').value, 10) || 5,
            notify: $('#mon-notify').checked,
            notifyMinSeverity: $('#mon-sev').value,
        };
        persist(cfg, cfg.monitoring.enabled ? `Monitoring every ${cfg.monitoring.intervalMinutes} min` : 'Monitoring off');
    });

    $('#hook-add')?.addEventListener('click', () => {
        state.hookDraft = [...readHooks(), {id: '', name: '', type: 'slack', url: '', minSeverity: 'critical', enabled: true}];
        state.open.add('webhooks');
        render();
    });
    root.querySelectorAll('[data-hook-del]').forEach(b => b.addEventListener('click', () => {
        const list = readHooks();
        list.splice(Number(b.dataset.hookDel), 1);
        state.hookDraft = list;
        render();
    }));
    root.querySelectorAll('[data-hook-test]').forEach(b => b.addEventListener('click', async () => {
        const i = Number(b.dataset.hookTest);
        const msg = root.querySelector(`[data-hook-msg="${i}"]`);
        b.disabled = true;
        try {
            await SendTestWebhook(readHooks()[i]);
            msg.innerHTML = '<div class="alert alert-ok">Test message sent.</div>';
        } catch (err) { msg.innerHTML = `<div class="alert alert-bad">${esc(String(err))}</div>`; }
        b.disabled = false;
    }));
    $('#hook-save')?.addEventListener('click', () => {
        const cfg = cloneConfig();
        cfg.webhooks = readHooks();
        persist(cfg, 'Chat & webhook channels saved');
    });

    $('#rem-save')?.addEventListener('click', () => {
        const enabled = $('#rem-enabled').checked;
        if (enabled && !confirm('Allow PhantomOps to roll back Kubernetes and Nomad deployments when you approve it? Each rollback still needs your explicit confirmation.')) return;
        const cfg = cloneConfig();
        cfg.remediation = {enabled};
        persist(cfg, enabled ? 'Rollbacks allowed (with confirmation)' : 'Rollbacks disabled');
    });

    $('#save-general')?.addEventListener('click', () => {
        const cfg = cloneConfig();
        cfg.environment = $('#g-env').value.trim();
        cfg.windowMinutes = parseInt($('#g-window').value, 10) || 30;
        cfg.ignoreSubjects = $('#g-ignore').value.split(',').map(s => s.trim()).filter(Boolean);
        persist(cfg, 'Settings saved');
    });

    // modal
    root.querySelectorAll('[data-close]').forEach(b => b.addEventListener('click', closeForm));
    $('#overlay')?.addEventListener('mousedown', e => { if (e.target.id === 'overlay') closeForm(); });
    root.querySelectorAll('[data-type]').forEach(b => b.addEventListener('click', () => {
        state.form = {type: b.dataset.type, conn: blankConn(b.dataset.type), editing: false, error: ''};
        render();
    }));
    $('#hostkey-check')?.addEventListener('click', async e => {
        const out = $('#hostkey-out');
        e.target.disabled = true; e.target.textContent = 'Checking…';
        out.innerHTML = '';
        try { drawHostKey(out, await InspectSSHHostKey(readForm())); }
        catch (err) { out.innerHTML = `<div class="alert alert-bad">${esc(String(err).replace(/^Error: /, ''))}</div>`; }
        e.target.disabled = false; e.target.textContent = 'Check host key';
    });
    // Event delegation: the Trust button only exists after a check.
    $('#hostkey-out')?.addEventListener('click', async e => {
        const btn = e.target.closest('#hostkey-trust');
        if (!btn) return;
        const out = $('#hostkey-out');
        btn.disabled = true; btn.textContent = 'Saving…';
        try { drawHostKey(out, await TrustSSHHostKey(readForm(), btn.dataset.fp)); }
        catch (err) {
            out.innerHTML = `<div class="alert alert-bad">${esc(String(err).replace(/^Error: /, ''))}</div>`;
        }
    });
    $('#form-test-btn')?.addEventListener('click', async e => {
        const out = $('#form-test');
        e.target.disabled = true; e.target.textContent = 'Testing…';
        out.innerHTML = '';
        const res = await TestConnection(readForm());
        out.innerHTML = `<div class="alert ${res.ok ? 'alert-ok' : 'alert-bad'}">${esc(res.message)}</div>`;
        e.target.disabled = false; e.target.textContent = 'Test connection';
    });
    $('#conn-form')?.addEventListener('submit', async e => {
        e.preventDefault();
        const c = readForm();
        const cfg = cloneConfig();
        if (state.form.editing) {
            cfg.connections = cfg.connections.map(x => x.id === c.id ? c : x);
        } else {
            cfg.connections.push(c);
        }
        const wasEditing = state.form.editing;
        const ok = await persist(cfg, wasEditing ? 'Connection updated' : 'Connection added');
        if (ok) state.form = null;
        else if (state.form) { state.form.error = state.error; state.error = ''; }
        render();
    });
}

// Support & community links. External links are opened in the system browser
// by the click handler in main.js (data-ext).
function renderSupport() {
    const base = 'https://github.com/Amanbig/PhantomOps';
    const link = (href, text) => `<a href="${href}" data-ext="${href}" class="ext-link">${text}</a>`;
    const body = `
        <p class="muted small" style="margin-top:0">PhantomOps is free and open source (Apache-2.0). If it saved you a bad night, you can help keep it going.</p>
        <ul class="support-links">
            <li>${link('https://github.com/sponsors/Amanbig', '♥ Sponsor on GitHub')}</li>
            <li>${link(base + '/issues/new/choose', 'Report a bug or request a feature')}</li>
            <li>${link(base + '/blob/main/CONTRIBUTING.md', 'Contribute')}</li>
            <li>${link(base + '/blob/main/SECURITY.md', 'Report a security issue')}</li>
        </ul>`;
    return acc('support', '♥ Support &amp; community', 'Sponsor · issues · contribute', '', body, 'support-card');
}

function closeForm() { state.form = null; render(); }

// mountSettings shows AI, monitoring, remediation, analysis and update settings
// as collapsible cards. Each visit starts with only the top card open (or the
// one named by `open`, e.g. 'updates' when coming from the update badge).
export async function mountSettings(el, {open = ''} = {}) {
    state.mode = 'settings';
    state.open = new Set([open || 'ai']);
    state.form = null;
    root = el;
    root.innerHTML = '<p class="muted">Loading…</p>';
    await refresh();
}

let gotoSettings = () => {};
export function onGotoSettings(cb) { gotoSettings = cb; }
