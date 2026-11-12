// PhantomOps' own title bar for the frameless window.
//
// The whole bar is a drag region for moving the window (Wails reads the
// --wails-draggable CSS property); interactive controls opt out. On macOS the
// native traffic-light buttons are kept (see main.go), so the bar leaves room
// for them and hides its own window buttons.
import {Environment, WindowMinimise, WindowToggleMaximise, WindowIsMaximised, Quit} from '../wailsjs/runtime/runtime';
import logoUrl from './assets/images/logo.svg';

const ICONS = {
    minimise: '<svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2 6.5h8" /></svg>',
    maximise: '<svg viewBox="0 0 12 12" aria-hidden="true"><rect x="2" y="2" width="8" height="8" rx="1" /></svg>',
    restore: '<svg viewBox="0 0 12 12" aria-hidden="true"><rect x="2" y="4" width="6" height="6" rx="1" /><path d="M4 4V3a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H8" /></svg>',
    close: '<svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2.5 2.5l7 7M9.5 2.5l-7 7" /></svg>',
};

// renderTitleBar returns the bar's markup; tabsHTML is placed in the middle.
export function renderTitleBar(tabsHTML) {
    return `
        <header class="titlebar" id="titlebar">
            <div class="tb-left">
                <img class="tb-logo" src="${logoUrl}" alt="" width="20" height="20" draggable="false">
                <span class="tb-name" title="Your infrastructure is haunted. Find out what's killing it.">PhantomOps</span>
                <span class="version" id="app-version"></span>
                <button class="update-badge no-drag" id="update-badge" hidden>⬆ Update available</button>
            </div>
            <nav class="tabs no-drag" aria-label="Sections">${tabsHTML}</nav>
            <div class="tb-right">
                <div class="win-controls no-drag" id="win-controls">
                    <button class="win-btn" id="win-min" title="Minimise" aria-label="Minimise">${ICONS.minimise}</button>
                    <button class="win-btn" id="win-max" title="Maximise" aria-label="Maximise">${ICONS.maximise}</button>
                    <button class="win-btn win-close" id="win-close" title="Close" aria-label="Close">${ICONS.close}</button>
                </div>
            </div>
        </header>`;
}

// initTitleBar wires the window buttons. It must be called after the markup
// returned by renderTitleBar is in the document.
export async function initTitleBar() {
    const bar = document.getElementById('titlebar');
    const maxBtn = document.getElementById('win-max');

    try {
        const env = await Environment();
        document.documentElement.classList.add(`platform-${env.platform}`);
    } catch { /* browser preview: keep the default layout */ }

    const syncMaximised = async () => {
        try {
            const max = await WindowIsMaximised();
            maxBtn.innerHTML = max ? ICONS.restore : ICONS.maximise;
            maxBtn.title = max ? 'Restore' : 'Maximise';
            maxBtn.setAttribute('aria-label', maxBtn.title);
            document.documentElement.classList.toggle('window-maximised', max);
        } catch { /* not running in the desktop app */ }
    };
    const toggle = () => { WindowToggleMaximise(); setTimeout(syncMaximised, 120); };

    document.getElementById('win-min').addEventListener('click', () => WindowMinimise());
    maxBtn.addEventListener('click', toggle);
    document.getElementById('win-close').addEventListener('click', () => Quit());

    // Double-clicking empty bar space maximises / restores, like a native title bar.
    bar.addEventListener('dblclick', e => {
        if (!e.target.closest('.no-drag, button, a, input, select')) toggle();
    });
    // Window managers can maximise too (keyboard shortcuts, snapping).
    let t;
    window.addEventListener('resize', () => { clearTimeout(t); t = setTimeout(syncMaximised, 150); });
    syncMaximised();
}
