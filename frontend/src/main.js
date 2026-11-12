import './style.css';
import './app.css';

import {mountIncidents} from './views/incidents.js';
import {mountConnections, mountSettings, onConnectionsChanged, onGotoSettings} from './views/connections.js';
import {mountActivity} from './views/activity.js';
import {mountHistory} from './views/history.js';
import {EventsOn} from '../wailsjs/runtime/runtime';
import {GetAppVersion, LastResult} from '../wailsjs/go/main/App';
import {onUpdateAvailable} from './views/updates.js';
import {renderTitleBar, initTitleBar} from './titlebar.js';
import {initSidebar, setActive, refreshSidebar} from './sidebar.js';
import {mountDashboard, leaveDashboard} from './views/dashboard.js';
import {mountOverview, leaveOverview} from './views/overview.js';

function show(view, opts = {}) {
    leaveDashboard();
    leaveOverview();
    setActive(view, view === 'dashboard' ? (opts.connId || '') : '');
    const el = document.getElementById('view');
    document.getElementById('app-body').scrollTop = 0;
    switch (view) {
        case 'history': return mountHistory(el, opts);
        case 'activity': return mountActivity(el);
        case 'connections': return mountConnections(el, opts);
        case 'settings': return mountSettings(el);
        case 'overview': return mountOverview(el, {
            openIncident: id => show('incidents', id ? {focusId: id} : {}),
            openDashboard: id => show('dashboard', {connId: id}),
            goToConnections: () => show('connections', {add: true}),
        });
        case 'dashboard': return mountDashboard(el, {
            connId: opts.connId,
            goToConnections: id => show('connections', {focusId: id}),
            goToIncident: id => show('incidents', {focusId: id}),
        });
        default: return mountIncidents(el, {goToConnections: () => show('connections'), ...opts});
    }
}

// Clicking a desktop notification opens the incident (or its history entry
// if it is no longer open).
EventsOn('notify:open', async id => {
    const last = await LastResult().catch(() => null);
    const open = (last?.incidents || []).some(i => i.id === id);
    show(open ? 'incidents' : 'history', {focusId: id});
});

document.querySelector('#app').innerHTML = `
    ${renderTitleBar()}
    <div class="app-main">
        <nav class="sidebar" id="sidebar" aria-label="Navigation"></nav>
        <div class="app-body" id="app-body">
            <div class="shell"><main id="view"></main></div>
        </div>
    </div>`;
initTitleBar();
initSidebar(document.getElementById('sidebar'), show);
onConnectionsChanged(refreshSidebar);
onGotoSettings(() => show('settings'));
EventsOn('scan:update', refreshSidebar);
show('overview');

GetAppVersion().then(v => { document.getElementById('app-version').textContent = v === 'dev' ? 'dev' : v; }).catch(() => {});
const badge = document.getElementById('update-badge');
onUpdateAvailable(info => {
    badge.hidden = false;
    badge.textContent = `⬆ ${info.latestVersion} available`;
});
badge.addEventListener('click', () => {
    show('settings');
    setTimeout(() => document.getElementById('updates-panel')?.scrollIntoView({block: 'start'}), 150);
});
