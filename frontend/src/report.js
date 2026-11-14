// Save / copy buttons for an incident report, shared by History and Incidents.
import {IncidentReport, SaveIncidentReport} from '../wailsjs/go/main/App';
import {ClipboardSetText} from '../wailsjs/runtime/runtime';
import {esc} from './util.js';

// reportButtons renders the two buttons and a message area for one incident.
export function reportButtons(id) {
    return `<div class="toolbar-actions hist-report">
        <button class="btn btn-small btn-secondary" data-report="save" data-id="${esc(id)}" title="Save a Markdown postmortem">Save report…</button>
        <button class="btn btn-small btn-secondary" data-report="copy" data-id="${esc(id)}" title="Copy the Markdown postmortem">Copy report</button>
        <span class="muted small" data-report-msg></span>
    </div>`;
}

// handleReportClick runs the action for a click on a [data-report] button and
// returns true if the click was one. Use it from a delegated click handler, so
// buttons that are re-rendered keep working.
export async function handleReportClick(target) {
    const b = target.closest?.('[data-report]');
    if (!b) return false;
    const msg = b.parentElement.querySelector('[data-report-msg]');
    b.disabled = true;
    msg.textContent = '';
    try {
        if (b.dataset.report === 'copy') {
            if (!await ClipboardSetText(await IncidentReport(b.dataset.id))) throw new Error('the clipboard is not available');
            msg.textContent = 'Copied. Log excerpts are left out and secrets are removed on a best-effort basis; review before sharing.';
        } else {
            const path = await SaveIncidentReport(b.dataset.id);
            if (path) msg.textContent = 'Saved to ' + path;
        }
    } catch (err) {
        msg.textContent = String(err).replace(/^Error: /, '');
    }
    b.disabled = false;
    return true;
}
