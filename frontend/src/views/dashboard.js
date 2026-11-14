import {GetSettings, ConnectionSnapshot, LastResult, TestConnection} from '../../wailsjs/go/main/App';
import {esc} from '../util.js';
import {typeInfo, scanHealth} from './connections.js';
import {lineChart} from '../charts.js';
import {logo} from '../logos.js';

const REFRESH_MS = 30000;
const DASHBOARD_TYPES = new Set(['docker', 'kubernetes', 'nomad', 'prometheus', 'loki', 'ssh']);

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

// tile renders a headline number. Pass text=true for word values (e.g. a
// service name), which get a smaller, truncating style.
function tile(label, value, sub = '', tone = '', text = false) {
    return `<div class="tile ${tone}"><div class="tile-value ${text ? 'tile-text' : ''}"${text ? ` title="${value}"` : ''}>${value}</div><div class="tile-label">${esc(label)}</div>
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

// ---------- nomad ----------

function renderNomad(s, ui) {
    const sm = s.summary;
    const jobs = s.jobs.map(j => ({...j, __cls: j.healthy ? '' : 'row-bad'}));
    const jobState = j => j.status === 'running' ? chip('running', j.healthy ? 'ok' : 'bad') : j.status === 'pending' ? chip('pending', 'warn') : chip(j.status || '?', j.healthy ? 'off' : 'bad');
    return `
        ${(s.partial || []).length ? `<div class="alert alert-warn"><span>Some data could not be read: ${esc(s.partial.join('; '))}</span></div>` : ''}
        <div class="tiles">
            ${tile('Nodes ready', `${sm.nodesReady}<span class="tile-of">/ ${sm.nodes}</span>`, `${esc(s.region || '')} · ${esc(s.datacenter || '')}`, sm.nodesReady < sm.nodes ? 'tone-bad' : 'tone-ok')}
            ${tile('Jobs running', `${sm.jobsRunning}<span class="tile-of">/ ${sm.jobs}</span>`, 'jobs', '')}
            ${tile('Jobs degraded', sm.jobsDegraded, 'failed, queued or stopped', sm.jobsDegraded ? 'tone-bad' : 'tone-ok')}
            ${tile('Allocations', sm.allocsRunning, `${sm.allocsFailed} failed or lost (24 h)`, sm.allocsFailed ? 'tone-warn' : '')}
            ${tile('Nomad', esc(s.version || '?'), 'agent version')}
        </div>
        <div class="panel"><div class="panel-head"><strong>Jobs</strong></div>
            ${table('jobs', [
                {key: 'id', label: 'Job', sort: j => j.namespace + '/' + j.id, html: j => `<span class="muted small">${esc(j.namespace)}/</span><strong>${esc(j.id)}</strong>`},
                {key: 'type', label: 'Type', sort: j => j.type, html: j => esc(j.type)},
                {key: 'status', label: 'Status', sort: j => j.status, html: jobState},
                {key: 'running', label: 'Running', cls: 'num', sort: j => j.running, html: j => String(j.running)},
                {key: 'failed', label: 'Failed', cls: 'num', sort: j => j.failed + j.lost, html: j => j.failed + j.lost ? `<span class="bad-text">${j.failed + j.lost}</span>` : '0'},
                {key: 'queued', label: 'Queued', cls: 'num', sort: j => j.queued, html: j => j.queued ? `<span class="warn-text">${j.queued}</span>` : '0'},
                {key: 'version', label: 'Version', cls: 'num', sort: j => j.version, html: j => `v${j.version}`},
            ], jobs, ui.sort)}
        </div>
        <div class="dash-grid">
            <div class="panel"><div class="panel-head"><strong>Failed &amp; restarting allocations</strong><span class="muted small">last 24 h</span></div>
                ${table('allocs', [
                    {key: 'job', label: 'Job', sort: a => a.job, html: a => `${esc(a.job)}<div class="muted small mono">${esc(a.id)} · ${esc(a.group)}</div>`},
                    {key: 'status', label: 'Status', sort: a => a.status, html: a => chip(a.status === 'running' ? `restarting ×${a.restarts}` : a.status, a.status === 'running' ? 'warn' : 'bad')},
                    {key: 'msg', label: 'Last event', html: a => `<span class="small">${esc(a.message || '—')}</span>`},
                    {key: 'when', label: 'When', sort: a => -new Date(a.modified), html: a => `${since(a.modified)} ago`},
                ], s.problemAllocs, ui.sort)}
            </div>
            <div class="panel"><div class="panel-head"><strong>Nodes</strong></div>
                ${table('nnodes', [
                    {key: 'name', label: 'Node', sort: n => n.name, html: n => `<strong>${esc(n.name)}</strong><div class="muted small">${esc(n.address)} · ${esc(n.datacenter)}</div>`},
                    {key: 'status', label: 'Status', sort: n => n.status, html: n => chip(n.status, n.status === 'ready' ? 'ok' : 'bad') + (n.drain ? ' ' + chip('draining', 'warn') : '') + (!n.eligible ? ' ' + chip('ineligible', 'warn') : '')},
                    {key: 'allocs', label: 'Running', cls: 'num', sort: n => n.runningAllocs, html: n => String(n.runningAllocs)},
                ], s.nodes, ui.sort)}
                ${s.deployments.length ? `<div class="panel-head" style="margin-top:14px"><strong>Deployments</strong></div>
                    <ul class="warn-list">${s.deployments.map(d => `<li>${chip(d.status, d.status === 'failed' ? 'bad' : 'warn')} <strong>${esc(d.job)}</strong> v${d.version}
                        <div class="small muted">${esc(d.description)}</div></li>`).join('')}</ul>` : ''}
            </div>
        </div>`;
}

// ---------- prometheus ----------

function renderPrometheus(s, ui) {
    const sm = s.summary;
    const targets = s.targets.map(t => ({...t, __cls: t.health === 'up' ? '' : 'row-bad'}));
    const chart = title => s.charts.find(c => c.title === title);
    const panel = (title, opts = {}) => {
        const c = chart(title);
        return `<div class="panel"><div class="panel-head"><strong>${esc(title)}</strong><span class="muted small">last hour</span></div>
            ${c ? lineChart(c.series, {unit: c.unit, ...opts}) : '<div class="chart-empty muted small">Not available on this server.</div>'}</div>`;
    };
    return `
        ${(s.partial || []).length ? `<div class="alert alert-warn"><span>Some data could not be read: ${esc(s.partial.join('; '))}</span></div>` : ''}
        <div class="tiles">
            ${tile('Targets up', `${sm.targetsUp}<span class="tile-of">/ ${sm.targets}</span>`, `${sm.jobs} jobs`, sm.targetsUp < sm.targets ? 'tone-bad' : 'tone-ok')}
            ${tile('Alerts firing', sm.firing, `${sm.pending} pending`, sm.firing ? 'tone-bad' : sm.pending ? 'tone-warn' : 'tone-ok')}
            ${tile('Prometheus', esc(s.version || '?'), 'server version')}
        </div>
        ${s.alerts.length ? `<div class="panel"><div class="panel-head"><strong>Alerts</strong></div>
            <ul class="warn-list">${s.alerts.map(a => `<li>${chip(a.state, a.state === 'firing' ? 'bad' : 'warn')} <strong>${esc(a.name)}</strong>
                ${a.severity ? `<span class="muted small">${esc(a.severity)}</span>` : ''} <span class="muted small">since ${since(a.since)}</span>
                ${a.summary ? `<div class="small muted">${esc(a.summary)}</div>` : ''}</li>`).join('')}</ul></div>` : ''}
        <div class="dash-grid">
            ${panel('Targets up', {max: 100})}
            ${panel('Firing alerts')}
        </div>
        ${panel('Samples ingested')}
        <div class="panel"><div class="panel-head"><strong>Scrape targets</strong></div>
            ${table('targets', [
                {key: 'job', label: 'Job', sort: t => t.job, html: t => `<strong>${esc(t.job)}</strong>`},
                {key: 'instance', label: 'Instance', sort: t => t.instance, html: t => `<span class="mono small">${esc(t.instance)}</span>`},
                {key: 'health', label: 'Health', sort: t => t.health, html: t => chip(t.health, t.health === 'up' ? 'ok' : 'bad')},
                {key: 'dur', label: 'Scrape', cls: 'num', sort: t => t.durationMs, html: t => `${t.durationMs.toFixed(0)} ms`},
                {key: 'last', label: 'Last scrape', sort: t => -new Date(t.lastScrape), html: t => isZero(t.lastScrape) ? '—' : `${since(t.lastScrape)} ago`},
                {key: 'err', label: 'Error', html: t => t.lastError ? `<span class="small bad-text">${esc(t.lastError)}</span>` : '<span class="muted">—</span>'},
            ], targets, ui.sort)}
        </div>`;
}

// ---------- loki ----------

function renderLoki(s, ui) {
    const sm = s.summary;
    const rate = sm.lines ? (sm.errors / sm.lines) * 100 : 0;
    const services = s.services.map(v => ({...v, __cls: v.errorRate >= 50 && v.errors > 0 ? 'row-bad' : ''}));
    return `
        ${(s.partial || []).length ? `<div class="alert alert-warn"><span>Some data could not be read: ${esc(s.partial.join('; '))}</span></div>` : ''}
        <div class="tiles">
            ${tile('Services', sm.services, 'sending logs (1 h)')}
            ${tile('Log lines', sm.lines.toLocaleString(), 'last hour')}
            ${tile('Error lines', sm.errors.toLocaleString(), `${rate.toFixed(1)}% of all lines`, sm.errors ? 'tone-warn' : 'tone-ok')}
            ${tile('Noisiest', sm.noisiest ? esc(sm.noisiest) : '—', 'most error lines', sm.noisiest ? 'tone-bad' : '', true)}
            ${tile('Loki', esc(s.version || '?'), 'server version')}
        </div>
        <div class="panel"><div class="panel-head"><strong>Error lines per service</strong><span class="muted small">per 5 minutes · last hour</span></div>
            ${lineChart(s.errorSeries, {})}</div>
        <div class="dash-grid">
            <div class="panel"><div class="panel-head"><strong>Services</strong><span class="muted small">last hour</span></div>
                ${table('services', [
                    {key: 'name', label: 'Service', sort: v => v.name, html: v => `<strong>${esc(v.name)}</strong>`},
                    {key: 'lines', label: 'Lines', cls: 'num', sort: v => v.lines, html: v => v.lines.toLocaleString()},
                    {key: 'errors', label: 'Errors', cls: 'num', sort: v => v.errors, html: v => v.errors ? `<span class="bad-text">${v.errors.toLocaleString()}</span>` : '0'},
                    {key: 'rate', label: 'Error rate', cls: 'w-meter', sort: v => v.errorRate, html: v => bar(v.errorRate, 100, `${v.errorRate.toFixed(1)}%`, {warn: 5, crit: 25})},
                ], services, ui.sort)}
            </div>
            <div class="panel"><div class="panel-head"><strong>Recent error lines</strong></div>
                ${s.recentErrors.length ? `<ul class="log-list">${s.recentErrors.map(l => `<li><span class="muted small">${new Date(l.time).toISOString().slice(11, 19)}</span>
                    <span class="chip">${esc(l.service)}</span><div class="mono small">${esc(l.line)}</div></li>`).join('')}</ul>` : '<p class="muted small">No error lines in the last hour.</p>'}
            </div>
        </div>`;
}

// ---------- ssh ----------

function uptime(sec) {
    const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
    return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`;
}

function renderSSH(s, ui) {
    const m = s.memory;
    const used = m.total - m.available, swapUsed = m.swapTotal - m.swapFree;
    const loadRatio = s.cpus ? s.load[0] / s.cpus : 0;
    const fullest = s.disks[0];
    const missing = s.ports.filter(p => !p.listening);
    return `
        ${(s.partial || []).length ? `<div class="alert alert-warn"><span>Some checks could not run: ${esc(s.partial.join('; '))}</span></div>` : ''}
        <div class="tiles">
            ${tile('Load (1 min)', s.load[0].toFixed(2), `${s.cpus} CPUs · ${s.load[1].toFixed(2)} / ${s.load[2].toFixed(2)} (5/15 min)`, loadRatio >= 2 ? 'tone-bad' : loadRatio >= 1 ? 'tone-warn' : 'tone-ok')}
            ${tile('Memory used', `${Math.round(pct(used, m.total))}%`, `${bytes(used)} of ${bytes(m.total)}`, pct(used, m.total) >= 95 ? 'tone-bad' : pct(used, m.total) >= 90 ? 'tone-warn' : 'tone-ok')}
            ${tile('Swap used', m.swapTotal ? `${Math.round(pct(swapUsed, m.swapTotal))}%` : '—', m.swapTotal ? `${bytes(swapUsed)} of ${bytes(m.swapTotal)}` : 'no swap', pct(swapUsed, m.swapTotal) >= 80 ? 'tone-bad' : pct(swapUsed, m.swapTotal) >= 50 ? 'tone-warn' : '')}
            ${tile('Fullest disk', fullest ? `${fullest.usePercent}%` : '—', fullest ? esc(fullest.mount) : '', fullest?.usePercent >= 95 ? 'tone-bad' : fullest?.usePercent >= 85 ? 'tone-warn' : 'tone-ok')}
            ${tile('Uptime', uptime(s.uptimeSec), esc(s.hostname || ''))}
        </div>
        <div class="panel"><div class="panel-head"><strong>Host</strong></div>
            <div class="plan-grid">
                <div class="label">Hostname</div><div>${esc(s.hostname || '—')}</div>
                <div class="label">OS</div><div>${esc(s.os || '—')}</div>
                <div class="label">Kernel</div><div class="mono small">${esc(s.kernel || '—')}</div>
                <div class="label">Services</div><div>${!s.systemd ? '<span class="muted">systemd not available on this host</span>'
                    : s.failedUnits.length ? s.failedUnits.map(u => chip(u, 'bad')).join(' ') : chip('no failed units', 'ok')}</div>
                ${s.ports.length ? `<div class="label">Ports</div><div>${s.ports.map(p => chip(`${p.port} ${p.listening ? 'listening' : 'not listening'}`, p.listening ? 'ok' : 'bad')).join(' ')}</div>` : ''}
            </div>
            ${missing.length ? `<div class="alert alert-bad">Expected port${missing.length > 1 ? 's' : ''} ${missing.map(p => p.port).join(', ')} ${missing.length > 1 ? 'are' : 'is'} not listening.</div>` : ''}
        </div>
        <div class="dash-grid">
            <div class="panel"><div class="panel-head"><strong>Disks</strong></div>
                ${table('disks', [
                    {key: 'mount', label: 'Mount', sort: d => d.mount, html: d => `<strong>${esc(d.mount)}</strong><div class="muted small mono">${esc(d.device)}</div>`},
                    {key: 'use', label: 'Used', cls: 'w-meter', sort: d => d.usePercent, html: d => bar(d.usePercent, 100, `${d.usePercent}% · ${bytes(d.usedBytes)} / ${bytes(d.sizeBytes)}`, {warn: 85, crit: 95})},
                ], s.disks, ui.sort)}
            </div>
            <div class="panel"><div class="panel-head"><strong>Top processes</strong><span class="muted small">by CPU</span></div>
                ${table('procs', [
                    {key: 'cmd', label: 'Command', sort: p => p.command, html: p => `<strong class="mono small">${esc(p.command)}</strong><div class="muted small">pid ${p.pid} · ${esc(p.user)}</div>`},
                    {key: 'cpu', label: 'CPU', cls: 'num', sort: p => p.cpu, html: p => `${p.cpu.toFixed(1)}%`},
                    {key: 'mem', label: 'Memory', cls: 'num', sort: p => p.mem, html: p => `${p.mem.toFixed(1)}%`},
                ], s.processes, ui.sort)}
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
    let timer = null, busy = false, loaded = false, gone = false, snap = null, error = '', conn = null, health = null, related = [], updated = null;

    async function load() {
        if (busy || !live()) return;
        busy = true;
        try {
            const [settings, last] = await Promise.all([GetSettings(), LastResult().catch(() => null)]);
            conn = (settings.config.connections || []).find(c => c.id === connId) || null;
            if (!conn) { error = 'This connection no longer exists.'; gone = true; snap = null; return; }
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
            loaded = true;
            if (live()) render();
        }
    }

    function render() {
        if (!live()) return;
        if (!conn) {
            // Until the first load finishes we cannot know whether it exists.
            root.innerHTML = !loaded
                ? '<div class="empty" role="status" aria-live="polite"><p class="ai-pending"><span class="spinner"></span> Loading connection…</p></div>'
                : `<div class="empty"><h3>${gone ? 'Connection not found' : "Couldn't load this connection"}</h3><p class="muted">${esc(error)}</p></div>`;
            return;
        }
        const t = typeInfo(conn.type);
        const target = conn.type === 'docker' ? (conn.sshConnection ? `via SSH · ${conn.remoteSocket || '/var/run/docker.sock'}` : (conn.socket || '/var/run/docker.sock')) : conn.type === 'kubernetes' ? ((conn.sshConnection ? 'via SSH · ' : '') + (conn.context ? `context ${conn.context}` : 'current kubectl context'))
            : conn.type === 'ssh' ? `${conn.user}@${conn.addr}` : (conn.sshConnection ? 'via SSH · ' : '') + conn.url;
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
            const renderers = {docker: renderDocker, kubernetes: renderKube, nomad: renderNomad, prometheus: renderPrometheus, loki: renderLoki, ssh: renderSSH};
            body = (renderers[snap.type] || (() => '<p class="muted">Unknown dashboard type.</p>'))(snap, ui);
        }

        const relatedHTML = related.length ? `<div class="panel related"><div class="panel-head"><strong>Open incidents from this connection (${related.length})</strong></div>
            <ul class="related-list">${related.map(i => `<li><span class="badge badge-${esc(i.severity)}">${esc(i.severity)}</span>
                <a href="#" data-incident="${esc(i.id)}">${esc(i.title)}</a></li>`).join('')}</ul></div>` : '';

        root.innerHTML = `
            <div class="toolbar dash-head">
                <div class="dash-title"><span class="dash-icon">${logo(conn.type, 34)}</span>
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
