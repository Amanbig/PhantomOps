import './style.css';
import './app.css';

import {mountIncidents} from './views/incidents.js';
import {mountConnections} from './views/connections.js';
import {mountActivity} from './views/activity.js';
import {mountHistory} from './views/history.js';
import {EventsOn} from '../wailsjs/runtime/runtime';

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
    <div class="shell">
        <header class="topbar">
            <div>
                <div class="brand">👻 PhantomOps</div>
                <div class="tagline">Your infrastructure is haunted. Find out what's killing it.</div>
            </div>
            <nav class="tabs">${TABS.map(t => `<button class="tab" data-tab="${t.id}">${t.label}</button>`).join('')}</nav>
        </header>
        <main id="view"></main>
    </div>`;
document.querySelectorAll('.tab').forEach(t => t.addEventListener('click', () => show(t.dataset.tab)));
show('incidents');
