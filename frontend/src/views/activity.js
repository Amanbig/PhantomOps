import {AuditLog, WebhookDeliveries} from '../../wailsjs/go/main/App';
import {esc} from '../util.js';

const PHASE = {approved: 'Approved', result: 'Result', denied: 'Denied'};
const HOOK_TYPE = {slack: 'Slack', discord: 'Discord', generic: 'Generic JSON'};
const HOOK_KIND = {new: 'New incident', reopened: 'Came back', escalated: 'Escalated', resolved: 'Resolved', summary: 'Summary', test: 'Test message'};

export async function mountActivity(root) {
    root.innerHTML = `<div class="toolbar"><h2 class="view-title">Activity</h2>
        <button class="btn btn-secondary" id="refresh">Refresh</button></div>
        <p class="muted small">Every change PhantomOps makes to your infrastructure is recorded here (and in an append-only file next to your config) — the intent is written before the action runs.</p>
        <div id="activity-body"><p class="muted">Loading…</p></div>
        <h3 class="section-title">Webhook deliveries</h3>
        <p class="muted small">The most recent messages sent to your Slack, Discord and generic webhook channels, and whether they arrived. Kept in memory only, so it starts empty each time the app starts. Webhook addresses are never shown.</p>
        <div id="hooks-body"><p class="muted">Loading…</p></div>`;
    const body = root.querySelector('#activity-body');
    const hooksBody = root.querySelector('#hooks-body');

    async function loadHooks() {
        try {
            const rows = (await WebhookDeliveries(100)) || [];
            hooksBody.innerHTML = rows.length ? `<table class="audit">
                <thead><tr><th>Time</th><th>Channel</th><th>Message</th><th>Result</th></tr></thead>
                <tbody>${rows.map(d => `<tr class="${d.skipped ? '' : (d.ok ? 'row-ok' : 'row-bad')}">
                    <td>${esc(new Date(d.at).toLocaleString())}</td>
                    <td>${esc(d.channel || '(unnamed)')}<div class="muted small">${esc(HOOK_TYPE[d.type] || d.type)}</div></td>
                    <td>${esc(d.title)}<div class="muted small">${esc(HOOK_KIND[d.kind] || d.kind)}${d.incidentId ? ` · incident #${esc(d.incidentId)}` : ''}</div></td>
                    <td>${d.skipped ? '<span class="muted">Not sent</span>' : (d.ok ? 'Delivered ✓' : 'Failed ✗')}
                        ${d.error ? `<div class="small">${esc(d.error)}</div>` : ''}
                        ${!d.skipped ? `<div class="muted small">${esc(d.durationMs)} ms</div>` : ''}</td>
                </tr>`).join('')}</tbody></table>`
                : '<div class="empty"><div class="empty-icon">💬</div><h3>No deliveries yet</h3><p class="muted">Nothing has been sent to a webhook channel since the app started. Add a channel under Settings → Chat &amp; webhooks.</p></div>';
        } catch (err) {
            hooksBody.innerHTML = `<p class="error">${esc(String(err))}</p>`;
        }
    }

    async function load() {
        try {
            const rows = (await AuditLog(100)) || [];
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
    const refreshAll = () => Promise.all([load(), loadHooks()]);
    root.querySelector('#refresh').addEventListener('click', refreshAll);
    await refreshAll();
}
