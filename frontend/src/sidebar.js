// Left navigation: monitoring views, connected services (with live status)
// and settings. Re-rendered whenever a scan, triage action or settings change
// happens (see refreshSidebar callers).
import {GetSettings, LastResult} from '../wailsjs/go/main/App';
import {esc} from './util.js';
import {typeInfo, scanHealth} from './views/connections.js';

const ICON = {
    incidents: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 2 20h20L12 3z"/><path d="M12 10v4"/><path d="M12 17h.01"/></svg>',
    history: '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>',
    activity: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 6h13M8 12h13M8 18h13"/><path d="M3 6h.01M3 12h.01M3 18h.01"/></svg>',
    settings: '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></svg>',
    plug: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 2v6M15 2v6"/><path d="M6 8h12v4a6 6 0 0 1-12 0V8z"/><path d="M12 18v4"/></svg>',
    plus: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>',
};

let host = null;
let go = () => {};
let current = 'incidents';
let currentConn = '';
let cached = null; // [settings, last] from the latest refresh

function item(view, label, icon, extra = '') {
    return `<button class="sb-item ${current === view ? 'active' : ''}" data-view="${view}" title="${esc(label)}">
        <span class="sb-icon">${icon}</span><span class="sb-label">${esc(label)}</span>${extra}</button>`;
}

function render(settings, last) {
    const conns = settings?.config?.connections || [];
    const health = scanHealth(last);
    const open = (last?.incidents || []).filter(i => !i.muted && !i.snoozedUntil);
    const crit = open.filter(i => i.severity === 'critical').length;
    const count = open.length ? `<span class="sb-count ${crit ? 'crit' : ''}" title="${open.length} open (${crit} critical)">${open.length}</span>` : '';

    host.innerHTML = `
        <div class="sb-section">
            <div class="sb-heading">Monitor</div>
            ${item('incidents', 'Incidents', ICON.incidents, count)}
            ${item('history', 'History', ICON.history)}
            ${item('activity', 'Activity', ICON.activity)}
        </div>
        <div class="sb-section sb-conns">
            <div class="sb-heading">Connections
                <button class="sb-add" data-view="connections" data-add="1" title="Add a connection" aria-label="Add a connection">${ICON.plus}</button></div>
            ${conns.length ? conns.map(c => {
                const h = c.enabled ? health[c.name] : null;
                const dot = !c.enabled ? 'dot-off' : h ? (h.ok ? 'dot-ok' : 'dot-bad') : 'dot-idle';
                const tip = !c.enabled ? 'Disabled' : h ? h.message : 'Not scanned yet';
                return `<button class="sb-item sb-conn ${current === 'connections' && currentConn === c.id ? 'active' : ''}" data-view="connections" data-conn="${esc(c.id)}" title="${esc(c.name)}: ${esc(tip)}">
                    <span class="sb-icon sb-emoji">${typeInfo(c.type).icon}</span>
                    <span class="sb-label">${esc(c.name)}</span>
                    <span class="dot ${dot}"></span></button>`;
            }).join('') : `<button class="sb-item sb-empty" data-view="connections">
                <span class="sb-icon">${ICON.plug}</span><span class="sb-label">Connect a service</span></button>`}
            ${conns.length ? item(currentConn ? 'connections-none' : 'connections', 'Manage connections', ICON.plug).replace('data-view="connections-none"', 'data-view="connections"') : ''}
        </div>
        <div class="sb-section sb-bottom">
            ${item('settings', 'Settings', ICON.settings)}
        </div>`;
}

// initSidebar renders into el; navigate(view, opts) is called on clicks.
export function initSidebar(el, navigate) {
    host = el;
    go = navigate;
    host.addEventListener('click', e => {
        const b = e.target.closest('[data-view]');
        if (!b) return;
        const opts = {};
        if (b.dataset.conn) opts.focusId = b.dataset.conn;
        if (b.dataset.add) opts.add = true;
        go(b.dataset.view, opts);
    });
    window.addEventListener('phantom:refresh', refreshSidebar);
    refreshSidebar();
}

// setActive highlights the current view (and connection, if any).
export function setActive(view, connId = '') {
    current = view;
    currentConn = connId;
    if (cached) render(...cached);
}

export async function refreshSidebar() {
    if (!host) return;
    cached = await Promise.all([GetSettings().catch(() => null), LastResult().catch(() => null)]);
    render(...cached);
}
