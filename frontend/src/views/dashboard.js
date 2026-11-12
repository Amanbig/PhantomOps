import {GetSettings, ConnectionSnapshot, LastResult, TestConnection} from '../../wailsjs/go/main/App';
import {esc} from '../util.js';
import {typeInfo, scanHealth} from './connections.js';

const REFRESH_MS = 30000;
const DASHBOARD_TYPES = new Set(['docker', 'kubernetes']);

// ---------- formatting ----------

function bytes(n) {
    if (n == null) return '—';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, v = Number(n);
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`;
}
const pct = (a, b) => (b > 0 ? Math.min(100, Math.max(0, (a / b) * 100)) : 0);
const isZero = t => !t || String(t).startsWith('0001');

function since(iso) {
    if (isZero(iso)) return '—';
    const s = Math.max(0, (Date.now() - new Date(iso)) / 1000);
    if (s < 3600) return `${Math.max(1, Math.round(s / 60))}m`;
    if (s < 172800) return `${Math.round(s / 3600)}h`;
    return `${Math.round(s / 86400)}d`;
}

function bar(value, max, label, {warn = 80, crit = 95} = {}) {
    if (max == null || !(max > 0)) return `<span class="muted">${esc(label)}</span>`;
    const p = pct(value, max);
    const cls = p >= crit ? 'crit' : p >= warn ? 'warn' : 'ok';
    return `<div class="meter" title="${esc(label)}"><span class="meter-fill ${cls}" style="width:${p.toFixed(1)}%"></span></div>
            <span class="meter-label">${esc(label)}</span>`;
}

function tile(label, value, sub = '', tone = '') {
    return `<div class="tile ${tone}"><div class="tile-value">${value}</div><div class="tile-label">${esc(label)}</div>
        ${sub ? `<div class="tile-sub">${sub}</div>` : ''}</div>`;
}

function chip(text, tone) { return `<span class="chip chip-${tone}">${esc(text)}</span>`; }

// ---------- sortable table ----------

// table renders rows with sortable headers. cols: [{key, label, sort(row), html(row), cls}]
function table(id, cols, rows, sortState) {
    const st = sortState[id] || {};
    let data = rows;
    const col = cols.find(c => c.key === st.key && c.sort);
    if (col) {
        data = [...rows].sort((a, b) => {
            const x = col.sort(a), y = col.sort(b);
            return (x > y ? 1 : x < y ? -1 : 0) * (st.dir === 'desc' ? -1 : 1);
        });
    }
    return `<div class="table-wrap"><table class="dtable" data-table="${id}">
        <thead><tr>${cols.map(c => `<th class="${c.cls || ''} ${c.sort ? 'sortable' : ''}" ${c.sort ? `data-sort="${c.key}"` : ''}>${esc(c.label)}${st.key === c.key ? (st.dir === 'desc' ? ' ↓' : ' ↑') : ''}</th>`).join('')}</tr></thead>
        <tbody>${data.length ? data.map(r => `<tr class="${r.__cls || ''}">${cols.map(c => `<td class="${c.cls || ''}">${c.html(r)}</td>`).join('')}</tr>`).join('')
            : `<tr><td colspan="${cols.length}" class="muted empty-row">Nothing to show</td></tr>`}</tbody>
    </table></div>`;
}

// ---------- docker ----------

function renderDocker(s, ui) {
    const sm = s.summary, e = s.engine;
    const f = ui.filter || 'all', q = (ui.q || '').toLowerCase();
    const problem = c => (c.state === 'running' && c.health === 'unhealthy') || c.state === 'restarting' || c.oomKilled || (c.state === 'exited' && c.exitCode !== 0 && c.exitCode !== 143);
    const rows = s.containers.filter(c =>
        (f === 'all' || (f === 'running' && c.state === 'running') || (f === 'stopped' && c.state !== 'running') || (f === 'problems' && problem(c))) &&
        (!q || `${c.name} ${c.image} ${c.compose}`.toLowerCase().includes(q))
    ).map(c => ({...c, __cls: problem(c) ? 'row-bad' : ''}));
    const nProblems = s.containers.filter(problem).length;
    const failedStops = s.containers.filter(c => c.state !== 'running' && (c.oomKilled || (c.exitCode !== 0 && c.exitCode !== 143))).length;

    const state = c => c.state === 'running' ? chip('running', 'ok') : c.state === 'restarting' ? chip('restarting', 'bad') :
        c.state === 'paused' ? chip('paused', 'warn') : chip(c.state, 'off');
    const health = c => !c.health ? '<span class="muted">—</span>' : chip(c.health, c.health === 'healthy' ? 'ok' : c.health === 'unhealthy' ? 'bad' : 'warn');

    return `
        <div class="tiles">
            ${tile('Running', `${sm.running}<span class="tile-of">/ ${sm.total}</span>`, 'containers', sm.running ? 'tone-ok' : '')}
            ${tile('Unhealthy', sm.unhealthy, 'failing healthchecks', sm.unhealthy ? 'tone-bad' : '')}
            ${tile('Stopped', sm.stopped, failedStops ? `${failedStops} exited with an error` : 'none failed', failedStops ? 'tone-warn' : '')}
            ${tile('Restarts', sm.restarts, 'across all containers', sm.restarts ? 'tone-warn' : '')}
            ${tile('Engine', esc(e.version || '?'), `${e.cpus} CPUs · ${bytes(e.memoryBytes)} · ${e.images} images`)}
        </div>
        <div class="panel">
            <div class="panel-head"><strong>Containers</strong>
                <div class="toolbar-actions">
                    <div class="seg" role="group" aria-label="Filter">${[['all', 'All'], ['running', 'Running'], ['problems', `Problems${nProblems ? ` (${nProblems})` : ''}`], ['stopped', 'Stopped']]
                        .map(([k, l]) => `<button class="seg-btn ${f === k ? 'active' : ''}" data-filter="${k}">${esc(l)}</button>`).join('')}</div>
                    <input class="search" id="dash-search" type="search" placeholder="Search name or image" value="${esc(ui.q || '')}">
                </div></div>
            ${table('containers', [
                {key: 'name', label: 'Name', sort: c => c.name, html: c => `<strong>${esc(c.name)}</strong>${c.compose ? `<div class="muted small">${esc(c.compose)}</div>` : ''}`},
                {key: 'state', label: 'State', sort: c => c.state, html: state},
                {key: 'health', label: 'Health', sort: c => c.health, html: health},
                {key: 'cpu', label: 'CPU', cls: 'num', sort: c => c.cpuPercent ?? -1, html: c => c.cpuPercent == null ? '<span class="muted">—</span>' : `${c.cpuPercent.toFixed(1)}%`},
                {key: 'mem', label: 'Memory', cls: 'w-meter', sort: c => c.memoryBytes ?? -1, html: c => c.memoryBytes == null ? '<span class="muted">—</span>'
                    : c.memoryLimit ? bar(c.memoryBytes, c.memoryLimit, `${bytes(c.memoryBytes)} / ${bytes(c.memoryLimit)}`) : `${bytes(c.memoryBytes)} <span class="muted small">no limit</span>`},
                {key: 'restarts', label: 'Restarts', cls: 'num', sort: c => c.restarts, html: c => c.restarts ? `<span class="warn-text">${c.restarts}</span>` : '0'},
                {key: 'up', label: 'Up / exit', sort: c => c.state === 'running' ? -new Date(c.startedAt) : 1e15,
                    html: c => c.state === 'running' ? since(c.startedAt) : `<span class="${c.exitCode && c.exitCode !== 143 ? 'bad-text' : 'muted'}">exit ${c.exitCode}${c.oomKilled ? ' · OOM' : ''}</span>`},
                {key: 'image', label: 'Image', sort: c => c.image, html: c => `<span class="mono small">${esc(c.image)}</span>`},
                {key: 'ports', label: 'Ports', html: c => (c.ports || []).length ? `<span class="mono small">${c.ports.map(esc).join('<br>')}</span>` : '<span class="muted">—</span>'},
            ], rows, ui.sort)}
        </div>`;
}

// ---------- kubernetes ----------

function renderKube(s, ui) {
    const nodesReady = s.nodes.filter(n => n.ready).length;
    const depsOK = s.deployments.filter(d => d.healthy).length;
    const deps = s.deployments.map(d => ({...d, __cls: d.healthy ? '' : 'row-bad'}));
    return `
        ${(s.partial || []).length ? `<div class="alert alert-warn"><span>Some data could not be read: ${esc(s.partial.join('; '))}</span></div>` : ''}
        <div class="tiles">
            ${tile('Nodes ready', `${nodesReady}<span class="tile-of">/ ${s.nodes.length}</span>`, esc(s.serverVersion || ''), nodesReady < s.nodes.length ? 'tone-bad' : 'tone-ok')}
            ${tile('Deployments healthy', `${depsOK}<span class="tile-of">/ ${s.deployments.length}</span>`, `${s.namespaces} namespaces`, depsOK < s.deployments.length ? 'tone-bad' : 'tone-ok')}
            ${tile('Pods running', `${s.pods.running}<span class="tile-of">/ ${s.pods.total}</span>`, `${s.pods.pending} pending · ${s.pods.failed} failed`, s.pods.pending || s.pods.failed ? 'tone-warn' : '')}
            ${tile('Problem pods', s.problemPods.length, 'crashing, pending or not ready', s.problemPods.length ? 'tone-bad' : '')}
            ${tile('Warnings', s.warnings.length, 'in the last hour', s.warnings.length ? 'tone-warn' : '')}
        </div>
        <div class="panel"><div class="panel-head"><strong>Nodes</strong>${s.metricsAvailable ? '' : '<span class="muted small">Usage unavailable: metrics-server is not installed</span>'}</div>
            ${table('nodes', [
                {key: 'name', label: 'Node', sort: n => n.name, html: n => `<strong>${esc(n.name)}</strong> <span class="muted small">${esc((n.roles || []).join(', '))}</span>`},
                {key: 'ready', label: 'Status', sort: n => n.ready, html: n => n.ready ? chip('Ready', 'ok') : chip('NotReady', 'bad')},
                {key: 'cpu', label: 'CPU', cls: 'w-meter', sort: n => (n.cpuUsedMillis ?? -1) / (n.cpuMillis || 1), html: n => n.cpuUsedMillis == null ? `<span class="muted">${(n.cpuMillis / 1000).toFixed(1)} cores</span>` : bar(n.cpuUsedMillis, n.cpuMillis, `${(n.cpuUsedMillis / 1000).toFixed(2)} / ${(n.cpuMillis / 1000).toFixed(1)} cores`)},
                {key: 'mem', label: 'Memory', cls: 'w-meter', sort: n => (n.memoryUsed ?? -1) / (n.memoryBytes || 1), html: n => n.memoryUsed == null ? `<span class="muted">${bytes(n.memoryBytes)}</span>` : bar(n.memoryUsed, n.memoryBytes, `${bytes(n.memoryUsed)} / ${bytes(n.memoryBytes)}`)},
                {key: 'pods', label: 'Pods', cls: 'num', sort: n => n.pods, html: n => String(n.pods)},
                {key: 'ver', label: 'Kubelet', html: n => `<span class="mono small">${esc(n.kubeletVersion)}</span>`},
            ], s.nodes, ui.sort)}
        </div>
        <div class="panel"><div class="panel-head"><strong>Deployments</strong></div>
            ${table('deployments', [
                {key: 'name', label: 'Deployment', sort: d => d.namespace + '/' + d.name, html: d => `<span class="muted small">${esc(d.namespace)}/</span><strong>${esc(d.name)}</strong>`},
                {key: 'ready', label: 'Ready', cls: 'w-meter', sort: d => pct(d.ready, d.desired), html: d => bar(d.ready, Math.max(d.desired, 1), `${d.ready} / ${d.desired}`, {warn: 101, crit: 101}).replace('meter-fill ok', d.healthy ? 'meter-fill ok' : 'meter-fill crit')},
                {key: 'updated', label: 'Up to date', cls: 'num', sort: d => d.updated, html: d => `${d.updated}`},
                {key: 'age', label: 'Age', sort: d => -new Date(d.created), html: d => since(d.created)},
                {key: 'images', label: 'Images', html: d => `<span class="mono small">${(d.images || []).map(esc).join('<br>')}</span>`},
            ], deps, ui.sort)}
        </div>
        <div class="dash-grid">
            <div class="panel"><div class="panel-head"><strong>Problem pods</strong></div>
                ${table('pods', [
                    {key: 'name', label: 'Pod', sort: p => p.namespace + '/' + p.name, html: p => `<span class="muted small">${esc(p.namespace)}/</span>${esc(p.name)}`},
                    {key: 'reason', label: 'Reason', sort: p => p.reason, html: p => chip(p.reason || p.phase, ['Pending', 'NotReady'].includes(p.reason || p.phase) ? 'warn' : 'bad')},
                    {key: 'restarts', label: 'Restarts', cls: 'num', sort: p => p.restarts, html: p => String(p.restarts)},
                    {key: 'age', label: 'Age', html: p => esc(p.age)},
                ], s.problemPods, ui.sort)}
            </div>
            <div class="panel"><div class="panel-head"><strong>Warnings</strong><span class="muted small">last hour</span></div>
                ${s.warnings.length ? `<ul class="warn-list">${s.warnings.map(w => `<li>
                    <div><span class="chip chip-warn">${esc(w.reason)}${w.count > 1 ? ` ×${w.count}` : ''}</span> <span class="mono small">${esc(w.object)}</span>
                        <span class="muted small">${since(w.time)} ago</span></div>
                    <div class="small muted">${esc(w.message)}</div></li>`).join('')}</ul>` : '<p class="muted small">No warnings in the last hour.</p>'}
            </div>
        </div>`;
}

// ---------- page ----------

// Each mount gets a token; navigating anywhere invalidates it, so a late
// refresh can never draw over another view or another connection.
let activeMount = 0;
export function leaveDashboard() { activeMount++; }

export async function mountDashboard(root, {connId, goToConnections, goToIncident}) {
    const me = ++activeMount;
    const live = () => me === activeMount;
    const ui = {sort: {}, filter: 'all', q: ''};
    let timer = null, busy = false, snap = null, error = '', conn = null, health = null, related = [], updated = null;

    async function load() {
        if (busy || !live()) return;
        busy = true;
        try {
            const [settings, last] = await Promise.all([GetSettings(), LastResult().catch(() => null)]);
            conn = (settings.config.connections || []).find(c => c.id === connId) || null;
            if (!conn) { error = 'This connection no longer exists.'; snap = null; return; }
            health = scanHealth(last)[conn.name] || null;
            related = (last?.incidents || []).filter(i => !i.muted && (i.timeline || []).some(e => e.origin === conn.name));
            if (!conn.enabled) { error = 'This connection is disabled. Enable it to see its dashboard.'; snap = null; return; }
            if (!DASHBOARD_TYPES.has(conn.type)) { error = ''; snap = null; return; }
            snap = await ConnectionSnapshot(connId);
            error = '';
            updated = new Date();
        } catch (err) {
            error = String(err).replace(/^Error: /, '');
            snap = null;
        } finally {
            busy = false;
            if (live()) render();
        }
    }

    function render() {
        if (!live()) return;
        if (!conn) {
            root.innerHTML = `<div class="empty"><h3>Connection not found</h3><p class="muted">${esc(error)}</p></div>`;
            return;
        }
        const t = typeInfo(conn.type);
        const target = conn.type === 'docker' ? (conn.socket || '/var/run/docker.sock') : conn.type === 'kubernetes' ? (conn.context ? `context ${conn.context}` : 'current kubectl context')
            : conn.type === 'ssh' ? `${conn.user}@${conn.addr}` : conn.url;
        const supported = DASHBOARD_TYPES.has(conn.type);
        const dot = !conn.enabled ? 'dot-off' : error ? 'dot-bad' : snap || health?.ok ? 'dot-ok' : health ? 'dot-bad' : 'dot-idle';

        let body;
        if (error) {
            body = `<div class="panel dash-error"><div class="panel-head"><strong>⚠ Can't read ${esc(conn.name)}</strong></div>
                <p class="error">${esc(error)}</p>
                <div class="toolbar-actions"><button class="btn btn-small btn-secondary" id="dash-test">Test connection</button>
                <button class="btn btn-small" id="dash-edit2">Edit connection</button></div><div id="dash-test-out"></div></div>`;
        } else if (!supported) {
            body = `<div class="panel"><p class="muted" style="margin:0">A detailed dashboard for ${esc(t.label)} connections is coming soon.
                ${health ? `Last scan: <strong class="${health.ok ? 'ok-text' : 'bad-text'}">${esc(health.message)}</strong>.` : ''}</p></div>`;
        } else if (!snap) {
            body = '<div class="panel"><p class="ai-pending"><span class="spinner"></span> Reading current state…</p></div>';
        } else {
            body = snap.type === 'docker' ? renderDocker(snap, ui) : renderKube(snap, ui);
        }

        const relatedHTML = related.length ? `<div class="panel related"><div class="panel-head"><strong>Open incidents from this connection (${related.length})</strong></div>
            <ul class="related-list">${related.map(i => `<li><span class="badge badge-${esc(i.severity)}">${esc(i.severity)}</span>
                <a href="#" data-incident="${esc(i.id)}">${esc(i.title)}</a></li>`).join('')}</ul></div>` : '';

        root.innerHTML = `
            <div class="toolbar dash-head">
                <div class="dash-title"><span class="dash-icon">${t.icon}</span>
                    <div><h2 class="view-title">${esc(conn.name)} <span class="dot ${dot}"></span></h2>
                        <div class="muted small">${esc(t.label)} · <span class="mono">${esc(target)}</span>${updated && snap ? ` · updated ${updated.toISOString().slice(11, 19)} UTC` : ''}</div></div></div>
                <div class="toolbar-actions">
                    ${supported && conn.enabled ? `<button class="btn btn-secondary" id="dash-refresh" ${busy ? 'disabled' : ''}>Refresh</button>` : ''}
                    <button class="btn btn-secondary" id="dash-edit">Edit connection</button>
                </div>
            </div>
            ${relatedHTML}
            ${body}`;
        bind();
    }

    function bind() {
        const $ = s => root.querySelector(s);
        $('#dash-refresh')?.addEventListener('click', load);
        $('#dash-edit')?.addEventListener('click', () => goToConnections(connId));
        $('#dash-edit2')?.addEventListener('click', () => goToConnections(connId));
        $('#dash-test')?.addEventListener('click', async e => {
            e.target.disabled = true;
            const r = await TestConnection(conn).catch(err => ({ok: false, message: String(err)}));
            $('#dash-test-out').innerHTML = `<div class="alert ${r.ok ? 'alert-ok' : 'alert-bad'}">${esc(r.message)}</div>`;
            e.target.disabled = false;
            if (r.ok) load();
        });
        root.querySelectorAll('[data-incident]').forEach(a => a.addEventListener('click', e => { e.preventDefault(); goToIncident(a.dataset.incident); }));
        root.querySelectorAll('[data-filter]').forEach(b => b.addEventListener('click', () => { ui.filter = b.dataset.filter; render(); }));
        const search = $('#dash-search');
        search?.addEventListener('input', () => {
            ui.q = search.value;
            const pos = search.selectionStart;
            render();
            const s2 = root.querySelector('#dash-search');
            s2.focus(); s2.setSelectionRange(pos, pos);
        });
        root.querySelectorAll('th[data-sort]').forEach(th => th.addEventListener('click', () => {
            const id = th.closest('table').dataset.table, key = th.dataset.sort;
            const cur = ui.sort[id];
            ui.sort[id] = {key, dir: cur?.key === key && cur.dir === 'asc' ? 'desc' : 'asc'};
            render();
        }));
    }

    render();
    await load();
    timer = setInterval(() => {
        if (!live()) { clearInterval(timer); return; }
        if (document.visibilityState === 'visible') load();
    }, REFRESH_MS);
    return () => clearInterval(timer);
}
