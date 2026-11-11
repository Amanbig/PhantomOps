import {PreviewRollback, ApproveRollback} from '../../wailsjs/go/main/App';
import {esc} from '../util.js';

// Opens the rollback review dialog for an incident. onDone() is called when
// the dialog closes after a rollback was attempted, so the caller can re-scan.
export async function openRollback(incident, {onDone, goToConnections}) {
    const overlay = document.createElement('div');
    overlay.className = 'overlay';
    overlay.innerHTML = '<div class="modal modal-wide" role="dialog" aria-modal="true" aria-label="Rollback review"></div>';
    document.body.appendChild(overlay);
    const modal = overlay.firstElementChild;
    let timer = null;
    let attempted = false;

    const close = () => {
        clearInterval(timer);
        overlay.remove();
        document.removeEventListener('keydown', onKey);
        if (attempted && onDone) onDone();
    };
    const onKey = e => { if (e.key === 'Escape') close(); };
    document.addEventListener('keydown', onKey);
    overlay.addEventListener('mousedown', e => { if (e.target === overlay) close(); });

    const head = title => `<div class="modal-head"><h3>${title}</h3>
        <button class="btn btn-small btn-secondary" data-close aria-label="Close">✕</button></div>`;
    const wireClose = () => modal.querySelectorAll('[data-close]').forEach(b => b.addEventListener('click', close));

    modal.innerHTML = head('Reviewing rollback…') + '<p class="ai-pending"><span class="spinner"></span> Inspecting the target. Nothing is changed yet.</p>';
    wireClose();

    let pv;
    try {
        pv = await PreviewRollback(incident.id);
    } catch (err) {
        const msg = String(err);
        modal.innerHTML = head('Rollback unavailable') + `<div class="alert alert-bad">${esc(msg)}</div>
            <div class="modal-actions">
                ${/turned off/.test(msg) ? '<button class="btn" id="goto-settings">Open settings</button>' : ''}
                <button class="btn btn-secondary" data-close>Close</button></div>`;
        wireClose();
        modal.querySelector('#goto-settings')?.addEventListener('click', () => { close(); goToConnections?.(); });
        return;
    }

    const plan = pv.plan;
    if (!plan.feasible) {
        modal.innerHTML = head(`Cannot roll back ${esc(plan.target)}`) + `
            <div class="alert alert-bad">${esc(plan.reason || 'Not feasible.')}</div>
            ${plan.current ? `<p class="muted small">Current: ${esc(plan.current)}</p>` : ''}
            <div class="modal-actions"><button class="btn btn-secondary" data-close>Close</button></div>`;
        wireClose();
        return;
    }

    const changes = (plan.changes || []).length
        ? `<ul class="reasoning">${plan.changes.map(c => `<li>${esc(c)}</li>`).join('')}</ul>`
        : '<p class="muted small">The exact differences could not be determined.</p>';
    const notes = (plan.notes || []).map(n => `<li>${esc(n)}</li>`).join('');

    modal.innerHTML = head(`Roll back <code>${esc(plan.target)}</code>?`) + `
        <div class="plan-grid">
            <div class="label">Platform</div><div>${esc(plan.platform)} · connection “${esc(plan.connection)}”</div>
            <div class="label">Now running</div><div>${esc(plan.current)}</div>
            <div class="label">Will return to</div><div>${esc(plan.previous)}</div>
        </div>
        <div class="label">What will change</div>
        ${changes}
        <div class="label">Exact action</div>
        <pre class="cmd">${esc(plan.command)}</pre>
        ${notes ? `<ul class="plan-notes">${notes}</ul>` : ''}
        <div class="alert alert-warn"><span>⚠ This changes live infrastructure. Check “What will change” above: the previous revision is not always the last <em>good</em> one.</span></div>
        <label><span>Type <strong>${esc(pv.confirmText)}</strong> to confirm</span>
            <input id="confirm-text" autocomplete="off" spellcheck="false" placeholder="${esc(pv.confirmText)}"></label>
        <div id="rb-status"></div>
        <div class="modal-actions">
            <span class="muted small" id="rb-expiry"></span>
            <button class="btn btn-secondary" data-close>Cancel</button>
            <button class="btn btn-danger-solid" id="rb-go" disabled>Roll back now</button>
        </div>`;
    wireClose();

    const input = modal.querySelector('#confirm-text');
    const go = modal.querySelector('#rb-go');
    const status = modal.querySelector('#rb-status');
    const expiry = modal.querySelector('#rb-expiry');
    input.focus();
    input.addEventListener('input', () => { go.disabled = input.value.trim() !== pv.confirmText; });

    let left = pv.expiresIn;
    const tick = () => {
        if (left <= 0) {
            clearInterval(timer);
            go.disabled = true;
            input.disabled = true;
            expiry.textContent = 'Preview expired. Close and try again.';
            return;
        }
        expiry.textContent = `Approval valid for ${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`;
        left--;
    };
    tick();
    timer = setInterval(tick, 1000);

    go.addEventListener('click', async () => {
        go.disabled = true;
        input.disabled = true;
        clearInterval(timer);
        expiry.textContent = '';
        attempted = true;
        status.innerHTML = '<p class="ai-pending"><span class="spinner"></span> Rolling back and waiting for the workload to become healthy… (up to ~90 s)</p>';
        try {
            const res = await ApproveRollback(pv.token, input.value.trim());
            status.innerHTML = `<div class="alert ${res.healthy ? 'alert-ok' : 'alert-warn'}">${esc(res.message)}</div>`;
        } catch (err) {
            status.innerHTML = `<div class="alert alert-bad">${esc(String(err))}</div>`;
        }
        modal.querySelector('.modal-actions').innerHTML = '<button class="btn" data-close>Close &amp; re-scan</button>';
        wireClose();
    });
}
