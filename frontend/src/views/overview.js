import {GetSettings, LastResult, GetHistory, GetMonitorStatus, Scan} from '../../wailsjs/go/main/App';
import {EventsOn} from '../../wailsjs/runtime/runtime';
import {esc} from '../util.js';
import {typeInfo, scanHealth} from './connections.js';
import {barChart} from '../charts.js';

const DAY = 24 * 3600 * 1000;
const SEV = {critical: 0, warning: 1, info: 2};
const isZero = t => !t || String(t).startsWith('0001');

// serviceOf pulls the affected service out of an incident title
// ("Unhealthy service: billing" -> "billing", "Anomalies on floci" -> "floci").
export function serviceOf(title) {
    const t = String(title || '');
    const i = t.lastIndexOf(': ');
    if (i >= 0) return t.slice(i + 2).trim();
    const m = t.match(/\bon (.+)$/);
    return m ? m[1].trim() : t;
}

// originsOf lists the connections that produced an incident's events.
function originsOf(inc) {
    return [...new Set((inc?.timeline || []).map(e => e.origin).filter(Boolean))];
}

function dur(ms) {
    if (!(ms > 0)) return '—';
    const m = Math.round(ms / 60000);
    if (m < 60) return `${Math.max(1, m)} min`;
    const h = ms / 3600000;
    return h < 48 ? `${h.toFixed(1)} h` : `${Math.round(h / 24)} days`;
}

function ago(iso) {
    if (isZero(iso)) return '—';
    const ms = Date.now() - new Date(iso);
    return ms < 60000 ? 'just now' : dur(ms) + ' ago';
}

// hourly buckets new incidents (by first seen) over the last 24 hours.
export function hourly(records, now = Date.now()) {
    const end = Math.ceil(now / 3600000) * 3600000;
    const buckets = Array.from({length: 24}, (_, i) => {
        const t = end - (24 - i) * 3600000;
        return {t, label: new Date(t).toISOString().slice(11, 16), values: {critical: 0, warning: 0}};
    });
    for (const r of records) {
        const t = new Date(r.firstSeen).getTime();
        if (!(t >= end - DAY) || t >= end) continue;
        const b = buckets[Math.min(23, Math.floor((t - (end - DAY)) / 3600000))];
        b.values[r.severity === 'critical' ? 'critical' : 'warning']++;
    }
    return buckets;
}

function tile(label, value, sub, tone = '') {
    return `<div class="tile ${tone}"><div class="tile-value">${value}</div><div class="tile-label">${esc(label)}</div>
        ${sub ? `<div class="tile-sub">${sub}</div>` : ''}</div>`;
}

let activeRender = null;
let listening = false;

// leaveOverview stops background scan updates from redrawing the Overview
// once the user has navigated elsewhere (the view container is shared).
export function leaveOverview() { activeRender = null; }

export async function mountOverview(root, {openIncident, openDashboard, goToConnections}) {
    let data = null, scanning = false;
    if (!listening) {
        listening = true;
        EventsOn('scan:update', () => activeRender?.());
    }

    async function load() {
        const [settings, last, history, mon] = await Promise.all([
            GetSettings().catch(() => null), LastResult().catch(() => null),
            GetHistory().catch(() => []), GetMonitorStatus().catch(() => null)]);
        data = {settings, last, history: history || [], mon};
        render();
    }

    function render() {
        if (!root.isConnected || activeRender !== load) return;
        const {settings, last, history, mon} = data;
        const conns = settings?.config?.connections || [];
        const scanned = last && !isZero(last.scannedAt);
        const health = scanHealth(last);
        const incidents = last?.incidents || [];
        const visible = incidents.filter(i => !i.muted && !i.snoozedUntil)
            .sort((a, b) => (SEV[a.severity] ?? 3) - (SEV[b.severity] ?? 3) || new Date(a.detectedAt) - new Date(b.detectedAt));
        const crit = visible.filter(i => i.severity === 'critical').length;
        const acked = visible.filter(i => i.ackBy).length;
        const snoozed = incidents.filter(i => !i.muted && i.snoozedUntil).length;
        const muted = incidents.filter(i => i.muted).length;

        const enabled = conns.filter(c => c.enabled);
        const healthy = enabled.filter(c => health[c.name]?.ok).length;
        const failing = enabled.filter(c => health[c.name] && !health[c.name].ok);

        const now = Date.now();
        const day = history.filter(r => now - new Date(r.lastSeen) <= DAY || now - new Date(r.firstSeen) <= DAY);
        const resolved = history.filter(r => r.status === 'resolved' && r.resolvedAt && now - new Date(r.resolvedAt) <= DAY);
        const mttr = resolved.length ? resolved.reduce((s, r) => s + (new Date(r.resolvedAt) - new Date(r.firstSeen)), 0) / resolved.length : 0;

        const perConn = {};
        for (const inc of visible) for (const o of originsOf(inc)) perConn[o] = (perConn[o] || 0) + 1;

        const noisy = {};
        for (const r of day) {
            const s = serviceOf(r.title);
            const n = noisy[s] || (noisy[s] = {service: s, count: 0, critical: 0, last: r.lastSeen, open: false});
            n.count += 1 + (r.reopened || 0);
            if (r.severity === 'critical') n.critical++;
            if (r.status === 'open') n.open = true;
            if (new Date(r.lastSeen) > new Date(n.last)) n.last = r.lastSeen;
        }
        const noisiest = Object.values(noisy).sort((a, b) => b.count - a.count || b.critical - a.critical).slice(0, 6);

        if (!conns.length) {
            root.innerHTML = `<div class="toolbar"><h2 class="view-title">Overview</h2></div>
                <div class="empty"><div class="empty-icon">👻</div><h3>Connect your first service</h3>
                <p class="muted">PhantomOps watches Docker, Kubernetes, Nomad, Prometheus, Loki and SSH hosts and tells you what is breaking and why.</p>
                <button class="btn" id="ov-connect">Connect a service</button></div>`;
            root.querySelector('#ov-connect').addEventListener('click', goToConnections);
            return;
        }

        const monLine = mon?.enabled ? `<span class="monitor on"><span class="dot dot-ok"></span> Monitoring every ${mon.intervalMinutes} min</span>`
            : '<span class="monitor off">Monitoring off</span>';

        root.innerHTML = `
            <div class="toolbar"><div><h2 class="view-title">Overview</h2>
                <div class="muted small">${scanned ? `Last scan ${new Date(last.scannedAt).toISOString().slice(11, 19)} UTC` : 'Not scanned yet'} · ${monLine}</div></div>
                <button class="btn" id="ov-scan" ${scanning ? 'disabled' : ''}>${scanning ? 'Scanning…' : 'Scan now'}</button></div>

            <div class="tiles">
                ${tile('Open incidents', visible.length, visible.length ? `${crit} critical · ${visible.length - crit} warning` : 'all clear', crit ? 'tone-bad' : visible.length ? 'tone-warn' : 'tone-ok')}
                ${tile('Connections healthy', `${healthy}<span class="tile-of">/ ${enabled.length}</span>`, failing.length ? `${failing.length} failing` : scanned ? 'all reachable' : 'waiting for a scan', failing.length ? 'tone-bad' : scanned ? 'tone-ok' : '')}
                ${tile('Resolved (24 h)', resolved.length, resolved.length ? `avg. ${dur(mttr)} to resolve` : 'none in the last day', '')}
                ${tile('Triaged', acked, `${snoozed} snoozed · ${muted} muted`, '')}
            </div>

            <div class="ov-grid">
                <div class="panel"><div class="panel-head"><strong>Needs attention</strong>
                    ${visible.length ? `<a href="#" class="small" id="ov-all">All incidents →</a>` : ''}</div>
                    ${visible.length ? `<ul class="ov-list">${visible.slice(0, 8).map(i => `<li>
                        <span class="badge badge-${esc(i.severity)}">${esc(i.severity)}</span>
                        <div class="ov-main"><a href="#" data-incident="${esc(i.id)}">${esc(i.title)}</a>
                            <div class="muted small">${esc(originsOf(i).join(', ') || i.environment)} · since ${ago(i.detectedAt)}${i.ackBy ? ` · <span class="ok-text">✓ ${esc(i.ackBy)}</span>` : ''}</div></div>
                    </li>`).join('')}</ul>${visible.length > 8 ? `<p class="muted small">and ${visible.length - 8} more</p>` : ''}`
                    : `<div class="ov-clear"><div class="empty-icon">✅</div><p class="muted">${scanned ? 'Nothing needs attention right now.' : 'Run a scan to check your infrastructure.'}</p></div>`}
                </div>
                <div class="panel"><div class="panel-head"><strong>Incidents, last 24 h</strong><span class="muted small">new per hour (UTC)</span></div>
                    ${barChart(hourly(history, now), [{key: 'warning', name: 'warning', color: '#f5a623'}, {key: 'critical', name: 'critical', color: '#ff4d5e'}])}
                </div>
            </div>

            <div class="panel"><div class="panel-head"><strong>Connections</strong></div>
                <div class="ov-conns">${conns.map(c => {
                    const h = c.enabled ? health[c.name] : null;
                    const dot = !c.enabled ? 'dot-off' : h ? (h.ok ? 'dot-ok' : 'dot-bad') : 'dot-idle';
                    const state = !c.enabled ? 'Disabled' : h ? (h.ok ? 'Healthy' : 'Failing') : 'Not scanned yet';
                    const n = perConn[c.name] || 0;
                    return `<button class="ov-conn ${h && !h.ok ? 'bad' : ''}" data-conn="${esc(c.id)}" title="${esc(h && !h.ok ? `${c.name}: ${h.message}` : `Open the ${c.name} dashboard`)}">
                        <span class="ov-conn-icon">${typeInfo(c.type).icon}</span>
                        <span class="ov-conn-main"><strong>${esc(c.name)}</strong>
                            <span class="muted small">${esc(typeInfo(c.type).label)}</span>
                            <span class="small ${h && !h.ok ? 'bad-text' : 'muted'} ov-conn-state"><span class="dot ${dot}"></span> ${esc(h && !h.ok ? h.message : state)}</span></span>
                        ${n ? `<span class="sb-count ${visible.some(i => i.severity === 'critical' && originsOf(i).includes(c.name)) ? 'crit' : ''}" title="${n} open incident(s)">${n}</span>` : ''}
                    </button>`;
                }).join('')}</div>
            </div>

            <div class="panel"><div class="panel-head"><strong>Noisiest services</strong><span class="muted small">incidents in the last 24 h</span></div>
                ${noisiest.length ? `<table class="dtable"><thead><tr><th>Service</th><th class="num">Incidents</th><th class="num">Critical</th><th>Last seen</th><th>Status</th></tr></thead><tbody>
                    ${noisiest.map(n => `<tr><td><strong>${esc(n.service)}</strong></td><td class="num">${n.count}</td>
                        <td class="num">${n.critical ? `<span class="bad-text">${n.critical}</span>` : '0'}</td><td>${ago(n.last)}</td>
                        <td>${n.open ? '<span class="chip chip-bad">open</span>' : '<span class="chip chip-ok">resolved</span>'}</td></tr>`).join('')}
                </tbody></table>` : '<p class="muted small">No incidents in the last 24 hours.</p>'}
            </div>`;

        root.querySelector('#ov-scan').addEventListener('click', async () => {
            scanning = true; render();
            try { await Scan(); } finally { scanning = false; }
            await load();
            window.dispatchEvent(new Event('phantom:refresh'));
        });
        root.querySelector('#ov-all')?.addEventListener('click', e => { e.preventDefault(); openIncident(''); });
        root.querySelectorAll('[data-incident]').forEach(a => a.addEventListener('click', e => { e.preventDefault(); openIncident(a.dataset.incident); }));
        root.querySelectorAll('[data-conn]').forEach(b => b.addEventListener('click', () => openDashboard(b.dataset.conn)));
    }

    activeRender = load;
    root.innerHTML = '<p class="muted">Loading…</p>';
    await load();
}
