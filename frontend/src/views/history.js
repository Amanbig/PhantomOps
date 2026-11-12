import {GetHistory, ClearResolvedHistory} from '../../wailsjs/go/main/App';
import {esc, clock} from '../util.js';

function duration(from, to) {
    const ms = new Date(to) - new Date(from);
    const m = Math.round(ms / 60000);
    if (m < 1) return '< 1 min';
    if (m < 60) return `${m} min`;
    const h = ms / 3600000;
    if (h < 48) return `${h.toFixed(1)} h`;
    return `${Math.round(h / 24)} days`;
}

// UTC everywhere, matching the incident timelines.
function when(iso) {
    const d = new Date(iso);
    const sameDay = d.toISOString().slice(0, 10) === new Date().toISOString().slice(0, 10);
    const hm = d.toISOString().slice(11, 16);
    return sameDay ? `${hm} UTC` : `${d.toLocaleDateString(undefined, {month: 'short', day: 'numeric', timeZone: 'UTC'})} ${hm} UTC`;
}

function renderDetail(r) {
    const inc = r.latest || {};
    const inv = inc.investigation || {};
    const tl = (inc.timeline || []).map(e => `<li><span class="event-time">${clock(e.time)} UTC</span>
        <strong>${esc(e.title)}</strong> <span class="muted">${esc(e.subject)}</span></li>`).join('');
    return `<div class="hist-detail">
        <div class="label">Likely root cause ${inv.source === 'ai' ? `<span class="badge badge-ai">🤖 AI · ${esc(inv.model)}</span>` : ''}</div>
        <p>${esc(inv.rootCause || '—')} ${inv.confidence ? `<span class="confidence">${Math.round(inv.confidence * 100)}%</span>` : ''}</p>
        <div class="label">Recommended action</div><p>${esc(inv.recommendedAction || '—')}</p>
        <div class="label">Last timeline</div><ol class="hist-timeline">${tl}</ol>
    </div>`;
}

export async function mountHistory(root, {focusId} = {}) {
    root.innerHTML = `<div class="toolbar"><h2 class="view-title">History</h2>
        <div class="toolbar-actions">
            <select id="hist-filter" aria-label="Filter">
                <option value="all">All</option><option value="open">Open</option><option value="resolved">Resolved</option>
            </select>
            <button class="btn btn-secondary" id="hist-clear">Clear resolved</button>
            <button class="btn btn-secondary" id="hist-refresh">Refresh</button>
        </div></div>
        <p class="muted small">Incidents are kept across scans and restarts. One resolves after it has been absent for two complete scans. Resolved incidents are kept for 30 days.</p>
        <div id="hist-body"><p class="muted">Loading…</p></div>`;
    const body = root.querySelector('#hist-body');
    const filter = root.querySelector('#hist-filter');
    const expanded = new Set(focusId ? [focusId] : []);
    let records = [];

    function render() {
        const rows = records.filter(r => filter.value === 'all' || r.status === filter.value);
        if (!rows.length) {
            body.innerHTML = `<div class="empty"><div class="empty-icon">🕰</div><h3>No incidents ${filter.value === 'all' ? 'yet' : 'here'}</h3>
                <p class="muted">Incidents appear here once a scan detects them.</p></div>`;
            return;
        }
        body.innerHTML = `<div class="hist-list">${rows.map(r => `
            <div class="hist-row ${r.status}" data-id="${esc(r.id)}">
                <button class="hist-head" aria-expanded="${expanded.has(r.id)}">
                    <span class="badge badge-${esc(r.severity)}">${esc(r.severity)}</span>
                    <span class="hist-title">${esc(r.title)}</span>
                    <span class="hist-status ${r.status}">${r.status === 'open' ? '● Open' : '✓ Resolved'}</span>
                    <span class="muted small hist-times">
                        ${esc(when(r.firstSeen))} · ${r.status === 'open' ? `open for ${duration(r.firstSeen, new Date())}` : `lasted ${duration(r.firstSeen, r.lastSeen)}`}
                        · ${r.scans} scan${r.scans === 1 ? '' : 's'}${r.reopened ? ` · came back ${r.reopened}×` : ''}
                    </span>
                </button>
                ${expanded.has(r.id) ? renderDetail(r) : ''}
            </div>`).join('')}</div>`;
        body.querySelectorAll('.hist-head').forEach(h => h.addEventListener('click', () => {
            const id = h.parentElement.dataset.id;
            expanded.has(id) ? expanded.delete(id) : expanded.add(id);
            render();
        }));
        if (focusId) {
            body.querySelector(`[data-id="${CSS.escape(focusId)}"]`)?.scrollIntoView({block: 'center'});
            focusId = null;
        }
    }

    async function load() {
        try {
            records = await GetHistory() || [];
            render();
        } catch (err) {
            body.innerHTML = `<p class="error">${esc(String(err))}</p>`;
        }
    }

    filter.addEventListener('change', render);
    root.querySelector('#hist-refresh').addEventListener('click', load);
    root.querySelector('#hist-clear').addEventListener('click', async () => {
        if (!confirm('Delete all resolved incidents from the history?')) return;
        await ClearResolvedHistory();
        await load();
    });
    await load();
}
