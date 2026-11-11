import {Scan} from '../../wailsjs/go/main/App';
import {esc, clock} from '../util.js';

const SOURCE_LABEL = {
    docker: 'Docker', kubernetes: 'Kubernetes', nomad: 'Nomad', prometheus: 'Prometheus',
    loki: 'Loki', ssh: 'SSH', deploy: 'Deploy', proxy: 'Proxy', database: 'Database',
};

function renderIncident(inc) {
    const inv = inc.investigation;
    const events = (inc.timeline || []).map((e, i) => `
        <li class="event sev-${esc(e.severity)}">
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
        <article class="incident">
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

            <section class="investigation">
                <h3>👻 Phantom's investigation</h3>
                <div class="label">Likely root cause</div>
                <p>${esc(inv.rootCause)}
                    <span class="confidence">${Math.round(inv.confidence * 100)}% confidence</span></p>
                ${(inv.reasoning || []).length ? `
                <div class="label">Evidence</div>
                <ul class="reasoning">${inv.reasoning.map(r => `<li>${esc(r)}</li>`).join('')}</ul>` : ''}
                <div class="label">Recommended action</div>
                <p>${esc(inv.recommendedAction)}</p>
                ${inv.action === 'rollback'
                    ? `<button class="btn btn-approve" id="approve" disabled title="Remediation arrives in a later step">Approve rollback${inv.target ? ' · ' + esc(inv.target) : ''}</button>`
                    : ''}
            </section>
        </article>`;
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
