import './style.css';
import './app.css';

import {ListIncidents} from '../wailsjs/go/main/App';

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
                <div class="label">Recommended action</div>
                <p>${esc(inv.recommendedAction)}</p>
                ${inv.action === 'rollback'
                    ? '<button class="btn btn-approve" id="approve" disabled title="Remediation arrives in a later step">Approve rollback</button>'
                    : ''}
            </section>
        </article>`;
}

async function main() {
    document.querySelector('#app').innerHTML = `
        <div class="shell">
            <header class="topbar">
                <span class="brand">👻 PhantomOps</span>
                <span class="tagline">Your infrastructure is haunted. Find out what's killing it.</span>
            </header>
            <main id="content"><p class="muted">Summoning incidents…</p></main>
        </div>`;

    const content = document.getElementById('content');
    try {
        const incidents = await ListIncidents();
        content.innerHTML = incidents.length
            ? incidents.map(renderIncident).join('')
            : '<p class="muted">No incidents. Suspiciously quiet…</p>';
    } catch (err) {
        console.error(err);
        content.innerHTML = `<p class="error">Failed to load incidents: ${esc(err)}</p>`;
    }
}

main();
