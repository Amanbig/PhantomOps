import {Scan, LastResult, GetMonitorStatus, AcknowledgeIncident, UnacknowledgeIncident, SnoozeIncident, UnsnoozeIncident, MuteIncident, UnmuteIncident} from '../../wailsjs/go/main/App';
import {EventsOn} from '../../wailsjs/runtime/runtime';
import {openRollback} from './rollback.js';
import {esc, clock} from '../util.js';
import {reportButtons, handleReportClick} from '../report.js';

const SOURCE_LABEL = {
    docker: 'Docker', kubernetes: 'Kubernetes', nomad: 'Nomad', prometheus: 'Prometheus',
    loki: 'Loki', ssh: 'SSH', deploy: 'Deploy', proxy: 'Proxy', database: 'Database',
};

const incidentsById = new Map();

function renderInvestigation(inc) {
    const inv = inc.investigation;
    const isAI = inv.source === 'ai';
    const badge = isAI
        ? `<span class="badge badge-ai" title="Refined by a language model; verified against the timeline">🤖 AI · ${esc(inv.model)}</span>`
        : '<span class="badge badge-rules" title="Deterministic rule engine">Rule-based</span>';
    return `
        <section class="investigation">
            <h3>👻 Phantom's investigation ${badge}</h3>
            ${inv.aiStatus === 'pending' ? '<div class="ai-pending"><span class="spinner"></span> AI is analysing this incident… showing the rule-based result meanwhile.</div>' : ''}
            ${inv.aiStatus === 'failed' ? `<div class="note note-bad">${esc(inv.aiNote)}</div>` : ''}
            <div class="label">Likely root cause</div>
            <p>${esc(inv.rootCause)}
                <span class="confidence">${Math.round(inv.confidence * 100)}% confidence</span></p>
            ${isAI && (inv.citedEvents || []).length ? `<div class="cited">Based on timeline events ${inv.citedEvents.map(n => '#' + n).join(', ')}</div>` : ''}
            ${(inv.reasoning || []).length ? `
            <div class="label">Evidence</div>
            <ul class="reasoning">${inv.reasoning.map(r => `<li>${esc(r)}</li>`).join('')}</ul>` : ''}
            <div class="label">Recommended action</div>
            <p>${esc(inv.recommendedAction)}</p>
            ${isAI && inv.aiStatus !== 'failed' && inv.aiNote ? `<div class="note">${esc(inv.aiNote)}</div>` : ''}
            ${isAI && inv.ruleBasedRootCause ? `<details class="rules-view"><summary>Rule-based hypothesis</summary><p>${esc(inv.ruleBasedRootCause)}</p></details>` : ''}
            ${inv.action === 'rollback'
                ? `<button class="btn btn-approve" data-approve="${esc(inc.id)}" title="Review the plan before anything changes">Review rollback${inv.target ? ' · ' + esc(inv.target) : ''}</button>`
                : ''}
        </section>`;
}

function ago(iso) {
    const m = Math.round((Date.now() - new Date(iso)) / 60000);
    if (m < 1) return 'just now';
    if (m < 60) return `${m} min ago`;
    const h = Math.round(m / 60);
    return h < 48 ? `${h} h ago` : `${Math.round(h / 24)} days ago`;
}

function utcTime(iso) {
    const d = new Date(iso);
    const sameDay = d.toISOString().slice(0, 10) === new Date().toISOString().slice(0, 10);
    return (sameDay ? '' : d.toLocaleDateString(undefined, {month: 'short', day: 'numeric', timeZone: 'UTC'}) + ' ') + d.toISOString().slice(11, 16) + ' UTC';
}

// Same key the backend uses for mute rules (environment + title).
const muteKey = inc => `${inc.environment}|${inc.title}`;

function renderTriage(inc) {
    const id = esc(inc.id);
    const ack = inc.ackBy
        ? `<span class="triage-ack" title="Acknowledged ${esc(utcTime(inc.ackAt))}">✓ Acknowledged by ${esc(inc.ackBy)} · ${esc(ago(inc.ackAt))}</span>
           <button class="btn-link" data-triage="unack" data-id="${id}">Undo</button>`
        : `<button class="btn btn-small btn-secondary" data-triage="ack" data-id="${id}" title="Let others know someone is on it">Acknowledge</button>`;
    return `<div class="triage">
        ${ack}
        <span class="snooze">
            <span class="muted small">Snooze</span>
            ${[[60, '1 h'], [240, '4 h'], [1440, '24 h']].map(([m, l]) =>
                `<button class="btn btn-small btn-secondary" data-triage="snooze" data-id="${id}" data-minutes="${m}" title="Hide and silence for ${l}">${l}</button>`).join('')}
        </span>
        <button class="btn btn-small btn-secondary" data-triage="mute" data-id="${id}" title="Hide and silence every “${esc(inc.title)}” incident until unmuted">Mute</button>
    </div>`;
}

function renderHidden(snoozed, muted) {
    if (!snoozed.length && !muted.length) return '';
    const parts = [snoozed.length && `${snoozed.length} snoozed`, muted.length && `${muted.length} muted`].filter(Boolean).join(' · ');
    const row = (inc, label, action) => `<li><span class="badge badge-${esc(inc.severity)}">${esc(inc.severity)}</span>
        <span class="hidden-title">${esc(inc.title)}</span> <span class="muted small">${label}</span> ${action}</li>`;
    return `<details class="hidden-incidents"><summary>${parts} hidden</summary><ul>
        ${snoozed.map(i => row(i, `snoozed until ${esc(utcTime(i.snoozedUntil))}`,
            `<button class="btn-link" data-triage="unsnooze" data-id="${esc(i.id)}">Show now</button>`)).join('')}
        ${muted.map(i => row(i, 'muted', `<button class="btn-link" data-triage="unmute" data-key="${esc(muteKey(i))}">Unmute</button>`)).join('')}
    </ul></details>`;
}

function renderIncident(inc) {
    incidentsById.set(inc.id, inc);
    const cited = new Set(inc.investigation.citedEvents || []);
    const events = (inc.timeline || []).map((e, i) => `
        <li class="event sev-${esc(e.severity)} ${cited.has(i + 1) ? 'event-cited' : ''}">
            <div class="event-marker"><span>${i + 1}</span></div>
            <div class="event-body">
                <div class="event-head">
                    <span class="event-time" title="UTC">${clock(e.time)} UTC</span>
                    <span class="event-title">${esc(e.title)}</span>
                    <span class="chip">${esc(SOURCE_LABEL[e.source] || e.source)}</span>
                </div>
                <div class="event-detail">${esc(e.detail)}</div>
            </div>
        </li>`).join('');

    return `
        <article class="incident" data-incident="${esc(inc.id)}">
            <header class="incident-head">
                <div>
                    <div class="incident-id">Incident #${esc(inc.id)}</div>
                    <h2>${esc(inc.title)}</h2>
                    <div class="incident-meta">
                        ${esc(inc.environment)} · ${inc.automatic ? 'Detected automatically' : 'Reported manually'}
                    </div>
                </div>
                <div class="badges">
                    <span class="badge badge-${esc(inc.severity)}">${esc(inc.severity)}</span>
                </div>
            </header>
            ${renderTriage(inc)}
            <ol class="timeline">${events}</ol>
            ${renderInvestigation(inc)}
            ${reportButtons(inc.id)}
        </article>`;
}

// Live updates: the backend emits ai:update when a model finishes analysing.
let aiListenerAttached = false;
function attachAIListener() {
    if (aiListenerAttached) return;
    aiListenerAttached = true;
    EventsOn('ai:update', ({id, investigation}) => {
        const inc = incidentsById.get(id);
        const el = document.querySelector(`[data-incident="${CSS.escape(id)}"]`);
        if (!inc || !el) return;
        inc.investigation = investigation;
        el.outerHTML = renderIncident(inc);
    });
}

function renderStatus(res) {
    const when = new Date(res.scannedAt).toISOString().slice(11, 19) + ' UTC';
    const sources = (res.collectors || []).map(c => {
        const failed = res.errors && res.errors[c];
        return `<span class="chip ${failed ? 'chip-bad' : 'chip-ok'}" title="${esc(failed || 'ok')}">${esc(c)}</span>`;
    }).join(' ');
    const warns = (res.warnings || []).map(w => `<div class="note">${esc(w)}</div>`).join('');
    const errs = Object.entries(res.errors || {}).map(([k, v]) => `<div class="note note-bad">${esc(k)}: ${esc(v)}</div>`).join('');
    return `<div class="status">Scanned at ${esc(when)} ${sources}</div>${errs}${warns}`;
}

// The mounted view's renderer, so background scan events can refresh it.
let activeRender = null;
let scanListenerAttached = false;
function attachScanListener() {
    if (scanListenerAttached) return;
    scanListenerAttached = true;
    EventsOn('scan:update', res => activeRender?.(res));
}

function relTime(iso) {
    const s = Math.round((new Date(iso) - new Date()) / 1000);
    if (s <= 0) return 'now';
    if (s < 60) return `in ${s}s`;
    return `in ${Math.round(s / 60)} min`;
}

async function monitorLine() {
    try {
        const st = await GetMonitorStatus();
        if (!st.enabled) return '<span class="monitor off">Monitoring off</span>';
        const next = st.nextScan && !st.nextScan.startsWith('0001') ? ` · next scan ${relTime(st.nextScan)}` : '';
        const notify = st.notify ? (st.notificationsError ? ' · <span class="note-bad">notifications unavailable</span>' : ' · notifications on') : '';
        return `<span class="monitor on"><span class="dot dot-ok"></span> Monitoring every ${st.intervalMinutes} min${next}${notify}</span>`;
    } catch { return ''; }
}

export async function mountIncidents(root, {goToConnections, focusId} = {}) {
    attachAIListener();
    attachScanListener();
    incidentsById.clear();
    root.innerHTML = `
        <div class="toolbar"><h2 class="view-title">Incidents</h2>
            <div class="toolbar-actions"><span id="monitor-line" class="small"></span>
            <button class="btn" id="scan">Scan now</button></div></div>
        <div id="incident-content"></div>`;
    const content = root.querySelector('#incident-content');
    const btn = root.querySelector('#scan');
    const refreshMonitor = async () => { const el = root.querySelector('#monitor-line'); if (el) el.innerHTML = await monitorLine(); };

    function render(res) {
        if (!root.isConnected || !root.contains(content)) return;
        incidentsById.clear();
        if (!(res.collectors || []).length) {
            content.innerHTML = `<div class="empty">
                <div class="empty-icon">🔌</div>
                <h3>No services connected</h3>
                <p class="muted">Connect Docker, Kubernetes, Nomad, Prometheus, Loki or an SSH host so Phantom has something to investigate.</p>
                <button class="btn" id="goto-conn">Connect services</button></div>`;
        } else {
            const all = res.incidents || [];
            const muted = all.filter(i => i.muted);
            const snoozed = all.filter(i => !i.muted && i.snoozedUntil);
            const visible = all.filter(i => !i.muted && !i.snoozedUntil);
            content.innerHTML = renderStatus(res) + (visible.length
                ? visible.map(renderIncident).join('')
                : `<div class="empty"><div class="empty-icon">👻</div>
                   <h3>${all.length ? 'Nothing needs attention' : 'No incidents detected'}</h3>
                   <p class="muted">${all.length ? 'Every open incident is snoozed or muted.' : 'Suspiciously quiet… Phantom is watching.'}</p></div>`)
                + renderHidden(snoozed, muted);
        }
        content.querySelector('#goto-conn')?.addEventListener('click', goToConnections);
        // Keep the sidebar's incident count and connection status in step.
        window.dispatchEvent(new Event('phantom:refresh'));
        if (focusId) {
            const el = content.querySelector(`[data-incident="${CSS.escape(focusId)}"]`);
            if (el) { el.classList.add('incident-focus'); el.scrollIntoView({block: 'start'}); }
            focusId = null;
        }
        refreshMonitor();
    }
    activeRender = render;

    async function scan() {
        btn.disabled = true;
        btn.textContent = 'Scanning…';
        try {
            render(await Scan());
        } catch (err) {
            console.error(err);
            content.innerHTML = `<p class="error">Scan failed: ${esc(err)}</p>`;
        } finally {
            btn.disabled = false;
            btn.textContent = 'Scan now';
        }
    }

    btn.addEventListener('click', scan);
    content.addEventListener('click', async e => {
        if (await handleReportClick(e.target)) return;
        const b = e.target.closest('[data-approve]');
        if (b) {
            const inc = incidentsById.get(b.dataset.approve);
            if (inc) openRollback(inc, {onDone: scan, goToConnections});
            return;
        }
        const t = e.target.closest('[data-triage]');
        if (!t) return;
        const {triage: action, id, minutes, key} = t.dataset;
        const inc = id && incidentsById.get(id);
        if (action === 'mute' && !confirm(`Mute “${inc?.title}”? It will be hidden and silent until you unmute it (in the hidden list here or in History).`)) return;
        t.disabled = true;
        try {
            switch (action) {
                case 'ack': await AcknowledgeIncident(id); break;
                case 'unack': await UnacknowledgeIncident(id); break;
                case 'snooze': await SnoozeIncident(id, Number(minutes)); break;
                case 'unsnooze': await UnsnoozeIncident(id); break;
                case 'mute': await MuteIncident(id); break;
                case 'unmute': await UnmuteIncident(key); break;
            }
            render(await LastResult());
        } catch (err) {
            t.disabled = false;
            alert(String(err));
        }
    });

    // Show the latest result immediately; scan only if there is none yet.
    const last = await LastResult().catch(() => null);
    if (last && last.scannedAt && !String(last.scannedAt).startsWith('0001')) render(last);
    else await scan();
    const timer = setInterval(() => root.contains(content) ? refreshMonitor() : clearInterval(timer), 15000);
}
