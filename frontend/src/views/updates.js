import {CheckForUpdates, InstallUpdate, RestartApp, GetAppVersion, GetUpdateProgress, GetSettings, SaveSettings, PendingUpdate} from '../../wailsjs/go/main/App';
import {EventsOn} from '../../wailsjs/runtime/runtime';
import {esc} from '../util.js';

// State survives re-renders of the Connections view.
const state = {info: null, error: '', checking: false, progress: null, asset: '', version: ''};
let host = null;

EventsOn('update:progress', p => { state.progress = p; draw(); });
EventsOn('update:available', info => { state.info = info; draw(); });

// Minimal, safe Markdown for release notes: everything is escaped first, then
// a few constructs (headings, bullets, bold, inline code, rules) are styled.
export function renderNotes(md) {
    const inline = s => esc(s)
        .replace(/`([^`]+)`/g, '<code>$1</code>')
        .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    const out = [];
    let list = false;
    for (const raw of String(md || '').split('\n')) {
        const line = raw.trimEnd();
        const bullet = line.match(/^\s*[-*] (.*)$/);
        if (!bullet && list) { out.push('</ul>'); list = false; }
        if (bullet) { if (!list) { out.push('<ul>'); list = true; } out.push(`<li>${inline(bullet[1])}</li>`); continue; }
        const h = line.match(/^(#{1,4}) (.*)$/);
        if (h) { out.push(`<h${Math.min(h[1].length + 2, 6)}>${inline(h[2])}</h${Math.min(h[1].length + 2, 6)}>`); continue; }
        if (/^-{3,}$/.test(line.trim())) { out.push('<hr>'); continue; }
        if (line.trim()) out.push(`<p>${inline(line)}</p>`);
    }
    if (list) out.push('</ul>');
    return out.join('');
}

function size(n) { return n > 0 ? `${(n / 1048576).toFixed(1)} MB` : ''; }

function draw() {
    if (!host || !host.isConnected) return;
    const {info, progress} = state;
    const busy = progress && ['downloading', 'verifying', 'installing'].includes(progress.status);
    let body = '';

    if (state.error) body += `<div class="alert alert-bad">${esc(state.error)}</div>`;

    if (info) {
        if (info.available) {
            const assets = info.assets || [];
            if (!state.asset) state.asset = (assets.find(a => a.isDefault) || assets[0] || {}).name || '';
            body += `<div class="alert alert-ok"><span>🎉 <strong>${esc(info.latestVersion)}</strong> is available${info.publishedAt ? ` (released ${esc(new Date(info.publishedAt).toLocaleDateString())})` : ''}.</span>
                <a href="${esc(info.releaseUrl)}" target="_blank" rel="noopener" class="small">View on GitHub</a></div>
                <details class="notes" open><summary>Release notes</summary><div class="notes-body">${renderNotes(info.releaseNotes)}</div></details>`;
            if (info.canInstall) {
                body += `<label>Package
                    <select id="upd-asset" ${busy ? 'disabled' : ''}>${assets.map(a =>
                        `<option value="${esc(a.name)}" ${a.name === state.asset ? 'selected' : ''}>${esc(a.name)}${a.isDefault ? ' (recommended)' : ''} ${size(a.size)}</option>`).join('')}</select>
                    <span class="muted small">Detected install: ${esc(info.installType)} on ${esc(info.platform)}.${info.needsElevation ? ' Administrator rights will be requested.' : ''} Every download is checked against the release's SHA256SUMS before it is installed.</span></label>`;
            } else {
                body += `<div class="note">${esc(info.reason)}</div>`;
            }
        } else {
            body += `<div class="note">${esc(info.reason || 'You are on the latest version.')}${info.latestVersion && !info.noReleases ? ` Latest release: ${esc(info.latestVersion)}.` : ''}</div>`;
        }
    }

    if (progress && progress.status !== 'idle') {
        const pct = Math.max(0, Math.min(100, progress.percentage || 0));
        body += `<div class="upd-progress ${esc(progress.status)}">
            ${busy ? `<div class="bar"><span style="width:${pct}%"></span></div>` : ''}
            <div class="small ${progress.status === 'error' ? 'error' : progress.status === 'ready' ? 'ok' : 'muted'}">${esc(progress.message || progress.status)}${busy && progress.totalBytes ? ` · ${size(progress.readBytes)} / ${size(progress.totalBytes)}` : ''}</div></div>`;
    }

    const canInstall = info && info.available && info.canInstall && !busy && progress?.status !== 'ready';
    host.innerHTML = `
        ${body}
        <label class="check"><input type="checkbox" id="upd-auto"> Check for updates automatically <span class="muted small">(an anonymous request to the GitHub Releases API at startup and every 12 hours; nothing is installed without you)</span></label>
        <div class="toolbar-actions">
            <button class="btn btn-small btn-secondary" id="upd-check" ${state.checking || busy ? 'disabled' : ''}>${state.checking ? 'Checking…' : 'Check for updates'}</button>
            ${canInstall ? '<button class="btn btn-small" id="upd-install">Download &amp; install</button>' : ''}
            ${progress?.status === 'ready' ? '<button class="btn btn-small" id="upd-restart">Restart now</button>' : ''}
        </div>`;
    bind();
}

async function check() {
    state.checking = true; state.error = ''; draw();
    try { state.info = await CheckForUpdates(); state.asset = ''; }
    catch (err) { state.error = String(err); }
    state.checking = false; draw();
}

function bind() {
    const $ = s => host.querySelector(s);
    GetSettings().then(s => { const el = $('#upd-auto'); if (el) el.checked = !s.config.updates?.autoCheckDisabled; }).catch(() => {});
    $('#upd-auto')?.addEventListener('change', async e => {
        try {
            const s = await GetSettings();
            const cfg = s.config;
            cfg.updates = {autoCheckDisabled: !e.target.checked};
            await SaveSettings(cfg);
        } catch (err) { state.error = String(err); draw(); }
    });
    $('#upd-check')?.addEventListener('click', check);
    $('#upd-asset')?.addEventListener('change', e => { state.asset = e.target.value; });
    $('#upd-install')?.addEventListener('click', async () => {
        if (!confirm(`Download and install ${state.asset}?`)) return;
        state.error = '';
        state.progress = {status: 'downloading', percentage: 0, message: 'Starting…'}; draw();
        try { await InstallUpdate(state.asset); }
        catch (err) { state.progress = {status: 'error', message: String(err)}; }
        draw();
    });
    $('#upd-restart')?.addEventListener('click', async () => {
        try { await RestartApp(); } catch (err) { state.error = String(err); draw(); }
    });
}

// mountUpdates renders the panel into el (call after each Connections render).
export async function mountUpdates(el) {
    host = el;
    if (!state.version) state.version = await GetAppVersion().catch(() => '');
    if (!state.progress) state.progress = await GetUpdateProgress().catch(() => null);
    draw();
}

// onUpdateAvailable calls cb with an available update: immediately if the
// backend already found one (e.g. before this page loaded), and on every
// later automatic or manual check that finds one.
export function onUpdateAvailable(cb) {
    EventsOn('update:available', cb);
    PendingUpdate().then(info => {
        if (info && info.available) {
            if (!state.info) state.info = info;
            cb(info);
        }
    }).catch(() => {});
}
