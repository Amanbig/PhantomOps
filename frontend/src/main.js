import './style.css';
import './app.css';

import {Scan, DemoIncident} from '../wailsjs/go/main/App';

const SOURCE_LABEL = {
    docker: 'Docker', kubernetes: 'Kubernetes', nomad: 'Nomad', prometheus: 'Prometheus',
    loki: 'Loki', ssh: 'SSH', deploy: 'Deploy', proxy: 'Proxy', database: 'Database',
};

function esc(s) {
    return String(s ?? '').replace(/[&<>"']/g, c => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
}

function clock(iso) {
    const d = new Date(iso);
    const p = n => String(n).padStart(2, '0');
    return `${p(d.getUTCHours())}:${p(d.getUTCMinutes())}`;
}

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
                    ${inc.demo ? '<span class="badge badge-demo">Demo data</span>' : ''}
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
    const warns = [...(res.warnings || [])].map(w => `<div class="note">${esc(w)}</div>`).join('');
    const errs = Object.entries(res.errors || {}).map(([k, v]) => `<div class="note note-bad">${esc(k)}: ${esc(v)}</div>`).join('');
    return `<div class="status">Scanned at ${esc(when)} ${sources}</div>${errs}${warns}`;
}

async function scan() {
    const content = document.getElementById('content');
    const btn = document.getElementById('scan');
    btn.disabled = true;
    btn.textContent = 'Scanning…';
    try {
        const res = await Scan();
        const incidents = res.incidents || [];
        content.innerHTML = renderStatus(res) + (incidents.length
            ? incidents.map(renderIncident).join('')
            : `<div class="empty"><p class="muted">No incidents detected. Suspiciously quiet…</p>
               <button class="btn btn-secondary" id="demo">View demo incident</button></div>`);
        const demoBtn = document.getElementById('demo');
        if (demoBtn) {
            demoBtn.onclick = async () => {
                content.innerHTML = renderStatus(res) + renderIncident(await DemoIncident());
            };
        }
    } catch (err) {
        console.error(err);
        content.innerHTML = `<p class="error">Scan failed: ${esc(err)}</p>`;
    } finally {
        btn.disabled = false;
        btn.textContent = 'Scan now';
    }
}

function main() {
    document.querySelector('#app').innerHTML = `
        <div class="shell">
            <header class="topbar">
                <div>
                    <div class="brand">👻 PhantomOps</div>
                    <div class="tagline">Your infrastructure is haunted. Find out what's killing it.</div>
                </div>
                <button class="btn" id="scan">Scan now</button>
            </header>
            <main id="content"></main>
        </div>`;
    document.getElementById('scan').onclick = scan;
    scan();
}

main();
