import './style.css';
import './app.css';

import {mountIncidents} from './views/incidents.js';
import {mountConnections} from './views/connections.js';
import {mountActivity} from './views/activity.js';
import {mountHistory} from './views/history.js';
import {EventsOn} from '../wailsjs/runtime/runtime';
import {GetAppVersion} from '../wailsjs/go/main/App';
import {onUpdateAvailable} from './views/updates.js';
import {renderTitleBar, initTitleBar} from './titlebar.js';

const TABS = [
    {id: 'incidents', label: 'Incidents'},
    {id: 'history', label: 'History'},
    {id: 'connections', label: 'Connections'},
    {id: 'activity', label: 'Activity'},
];

function show(tab, opts = {}) {
    document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
    const view = document.getElementById('view');
    if (tab === 'incidents') mountIncidents(view, {goToConnections: () => show('connections'), ...opts});
    else if (tab === 'history') mountHistory(view, opts);
    else if (tab === 'activity') mountActivity(view);
    else mountConnections(view);
}

// Clicking a desktop notification opens the incident (or its history entry
// if it is no longer open).
EventsOn('notify:open', async id => {
    const {LastResult} = await import('../wailsjs/go/main/App');
    const last = await LastResult().catch(() => null);
    const open = (last?.incidents || []).some(i => i.id === id);
    show(open ? 'incidents' : 'history', {focusId: id});
});

document.querySelector('#app').innerHTML = `
    ${renderTitleBar(TABS.map(t => `<button class="tab" data-tab="${t.id}">${t.label}</button>`).join(''))}
    <div class="app-body" id="app-body">
        <div class="shell"><main id="view"></main></div>
    </div>`;
initTitleBar();
document.querySelectorAll('.tab').forEach(t => t.addEventListener('click', () => show(t.dataset.tab)));
show('incidents');

GetAppVersion().then(v => { document.getElementById('app-version').textContent = v === 'dev' ? 'dev' : v; }).catch(() => {});
const badge = document.getElementById('update-badge');
onUpdateAvailable(info => {
    badge.hidden = false;
    badge.textContent = `⬆ ${info.latestVersion} available`;
});
badge.addEventListener('click', () => {
    show('connections');
    setTimeout(() => document.getElementById('updates-panel')?.scrollIntoView({block: 'start'}), 150);
});
