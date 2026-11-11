import {AuditLog} from '../../wailsjs/go/main/App';
import {esc} from '../util.js';

const PHASE = {approved: 'Approved', result: 'Result', denied: 'Denied'};

export async function mountActivity(root) {
    root.innerHTML = `<div class="toolbar"><h2 class="view-title">Activity</h2>
        <button class="btn btn-secondary" id="refresh">Refresh</button></div>
        <p class="muted small">Every change PhantomOps makes to your infrastructure is recorded here (and in an append-only file next to your config) — the intent is written before the action runs.</p>
        <div id="activity-body"><p class="muted">Loading…</p></div>`;
    const body = root.querySelector('#activity-body');

    async function load() {
        try {
            const rows = await AuditLog(100);
            body.innerHTML = rows.length ? `<table class="audit">
                <thead><tr><th>Time</th><th>User</th><th>Step</th><th>Action</th><th>Target</th><th>Details</th></tr></thead>
                <tbody>${rows.map(r => `<tr class="${r.phase === 'result' ? (r.ok ? 'row-ok' : 'row-bad') : ''}">
                    <td>${esc(new Date(r.time).toLocaleString())}</td>
                    <td>${esc(r.user)}</td>
                    <td>${esc(PHASE[r.phase] || r.phase)}${r.phase === 'result' ? (r.ok ? ' ✓' : ' ✗') : ''}</td>
                    <td>${esc(r.action)}</td>
                    <td>${esc(r.target)}<div class="muted small">${esc(r.connection)} · incident #${esc(r.incidentId)}</div></td>
                    <td>${esc(r.message)}<div class="muted small mono">${esc(r.command)}</div></td>
                </tr>`).join('')}</tbody></table>`
                : '<div class="empty"><div class="empty-icon">📜</div><h3>Nothing yet</h3><p class="muted">No changes have been made by PhantomOps.</p></div>';
        } catch (err) {
            body.innerHTML = `<p class="error">${esc(String(err))}</p>`;
        }
    }
    root.querySelector('#refresh').addEventListener('click', load);
    await load();
}
