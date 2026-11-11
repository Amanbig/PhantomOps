import {Scan} from '../../wailsjs/go/main/App';
import {EventsOn} from '../../wailsjs/runtime/runtime';
import {esc, clock} from '../util.js';

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
                ? `<button class="btn btn-approve" id="approve" disabled title="Remediation arrives in a later step">Approve rollback${inv.target ? ' · ' + esc(inv.target) : ''}</button>`
                : ''}
        </section>`;
}

function renderIncident(inc) {
    incidentsById.set(inc.id, inc);
    const cited = new Set(inc.investigation.citedEvents || []);
    const events = (inc.timeline || []).map((e, i) => `
        <li class="event sev-${esc(e.severity)} ${cited.has(i + 1) ? 'event-cited' : ''}">
            <div class="event-marker"><span>${i + 1}</span></div>
            <div class="event-body">
                <div class="event-head">
                    <span class="event-time">${clock(e.time)}</span>
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
            <ol class="timeline">${events}</ol>
            ${renderInvestigation(inc)}
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
    const when = new Date(res.scannedAt).toLocaleTimeString();
    const sources = (res.collectors || []).map(c => {
        const failed = res.errors && res.errors[c];
        return `<span class="chip ${failed ? 'chip-bad' : 'chip-ok'}" title="${esc(failed || 'ok')}">${esc(c)}</span>`;
    }).join(' ');
    const warns = (res.warnings || []).map(w => `<div class="note">${esc(w)}</div>`).join('');
    const errs = Object.entries(res.errors || {}).map(([k, v]) => `<div class="note note-bad">${esc(k)}: ${esc(v)}</div>`).join('');
    return `<div class="status">Scanned at ${esc(when)} ${sources}</div>${errs}${warns}`;
}

export async function mountIncidents(root, {goToConnections}) {
    attachAIListener();
    incidentsById.clear();
    root.innerHTML = `
        <div class="toolbar"><h2 class="view-title">Incidents</h2>
            <button class="btn" id="scan">Scan now</button></div>
        <div id="incident-content"></div>`;
    const content = root.querySelector('#incident-content');
    const btn = root.querySelector('#scan');

    async function scan() {
        btn.disabled = true;
        btn.textContent = 'Scanning…';
        try {
            const res = await Scan();
            if (!(res.collectors || []).length) {
                content.innerHTML = `<div class="empty">
                    <div class="empty-icon">🔌</div>
                    <h3>No services connected</h3>
                    <p class="muted">Connect Docker, Kubernetes, Nomad, Prometheus, Loki or an SSH host so Phantom has something to investigate.</p>
                    <button class="btn" id="goto-conn">Connect services</button></div>`;
            } else {
                const incidents = res.incidents || [];
                content.innerHTML = renderStatus(res) + (incidents.length
                    ? incidents.map(renderIncident).join('')
                    : `<div class="empty"><div class="empty-icon">👻</div>
                       <h3>No incidents detected</h3>
                       <p class="muted">Suspiciously quiet… Phantom is watching.</p></div>`);
            }
            content.querySelector('#goto-conn')?.addEventListener('click', goToConnections);
        } catch (err) {
            console.error(err);
            content.innerHTML = `<p class="error">Scan failed: ${esc(err)}</p>`;
        } finally {
            btn.disabled = false;
            btn.textContent = 'Scan now';
        }
    }

    btn.addEventListener('click', scan);
    await scan();
}
