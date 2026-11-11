import './style.css';
import './app.css';

import {mountIncidents} from './views/incidents.js';
import {mountConnections} from './views/connections.js';

const TABS = [
    {id: 'incidents', label: 'Incidents'},
    {id: 'connections', label: 'Connections'},
];

function show(tab) {
    document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
    const view = document.getElementById('view');
    if (tab === 'incidents') mountIncidents(view, {goToConnections: () => show('connections')});
    else mountConnections(view);
}

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
