// Tiny dependency-free line chart for dashboard panels. Lines are drawn in a
// stretchable SVG with non-scaling strokes; axis labels are regular HTML, so
// text stays the same size however wide the panel is.
import {esc} from './util.js';

const COLORS = ['#a78bfa', '#5fd38d', '#f5a623', '#5b9dff', '#ff6b8b', '#4fd1c5', '#e0b0ff', '#c9d65f'];

function fmt(v, unit) {
    if (v == null || !isFinite(v)) return '—';
    if (unit === 'percent') return `${Math.round(v)}%`;
    const a = Math.abs(v);
    const s = a >= 1e6 ? `${(v / 1e6).toFixed(1)}M` : a >= 1e3 ? `${(v / 1e3).toFixed(1)}k` : a >= 10 || Number.isInteger(v) ? `${Math.round(v)}` : v.toFixed(2);
    return unit === 'per-second' ? `${s}/s` : s;
}

function hhmm(ts) { return new Date(ts * 1000).toISOString().slice(11, 16); }

// lineChart renders series ([{name, points: [[unixSec, value], ...]}]).
// Options: unit ("", "percent", "per-second"), height (px), max (fixed y max).
export function lineChart(series, {unit = '', height = 140, max} = {}) {
    const all = series.flatMap(s => s.points || []);
    if (!all.length) return '<div class="chart-empty muted small">No data in this time range yet.</div>';
    let x0 = Math.min(...all.map(p => p[0])), x1 = Math.max(...all.map(p => p[0]));
    if (x1 === x0) { x0 -= 60; x1 += 60; }
    let y1 = max ?? Math.max(...all.map(p => p[1])) * 1.1;
    if (!(y1 > 0)) y1 = 1;
    // Drawing space: 0..1000 wide, 0..100 tall, stretched to the box.
    const X = t => ((t - x0) / (x1 - x0)) * 1000;
    const Y = v => (1 - Math.min(v, y1) / y1) * 100;

    const lines = series.map((s, i) => {
        const c = COLORS[i % COLORS.length];
        const pts = (s.points || []).map(p => `${X(p[0]).toFixed(1)},${Y(p[1]).toFixed(2)}`);
        if (!pts.length) return '';
        // A lone point is drawn as a zero-length line with a round cap (a dot).
        const d = pts.length === 1 ? `${pts[0]} ${pts[0]}` : pts.join(' ');
        const w = pts.length === 1 ? 7 : 2;
        const last = s.points[s.points.length - 1];
        return `<polyline points="${d}" fill="none" stroke="${c}" stroke-width="${w}" stroke-linejoin="round" stroke-linecap="round" vector-effect="non-scaling-stroke"><title>${esc(s.name)}: ${esc(fmt(last[1], unit))} at ${hhmm(last[0])} UTC</title></polyline>`;
    }).join('');
    const grid = [0, 50, 100].map(y => `<line x1="0" x2="1000" y1="${y}" y2="${y}" class="grid" vector-effect="non-scaling-stroke"/>`).join('');
    const yLabels = [1, 0.5, 0].map(f => `<span>${esc(fmt(y1 * f, unit))}</span>`).join('');
    const xLabels = [x0, (x0 + x1) / 2, x1].map(t => `<span>${hhmm(t)}</span>`).join('');
    const named = series.length > 1 || (series[0] && series[0].name);
    const legend = named ? `<div class="chart-legend">${series.map((s, i) => {
        const last = (s.points || []).at(-1);
        return `<span title="${esc(s.name)}"><i style="background:${COLORS[i % COLORS.length]}"></i><em>${esc(s.name)}</em> <b>${esc(fmt(last?.[1], unit))}</b></span>`;
    }).join('')}</div>` : '';
    return `<div class="chart-box" style="--chart-h:${height}px">
        <div class="chart-y">${yLabels}</div>
        <svg class="chart" viewBox="0 0 1000 100" preserveAspectRatio="none" role="img" aria-label="Chart">${grid}${lines}</svg>
        <div class="chart-x">${xLabels}</div>
    </div>${legend}`;
}

// barChart renders stacked bars. buckets: [{label, values: {key: n}}];
// keys: [{key, name, color}] in stacking order (bottom first).
export function barChart(buckets, keys, {height = 120} = {}) {
    const totals = buckets.map(b => keys.reduce((n, k) => n + (b.values[k.key] || 0), 0));
    const max = Math.max(1, ...totals);
    const bars = buckets.map((b, i) => {
        let acc = 0;
        const segs = keys.map(k => {
            const v = b.values[k.key] || 0;
            if (!v) return '';
            const h = (v / max) * 100, y = 100 - ((acc + v) / max) * 100;
            acc += v;
            return `<rect x="${i * 10 + 1.5}" y="${y.toFixed(2)}" width="7" height="${h.toFixed(2)}" fill="${k.color}" rx="0.8"/>`;
        }).join('');
        const tip = `${b.label}: ${keys.map(k => `${b.values[k.key] || 0} ${k.name}`).join(', ')}`;
        return `<g><title>${esc(tip)}</title><rect x="${i * 10}" y="0" width="10" height="100" fill="transparent"/>${segs}</g>`;
    }).join('');
    const n = buckets.length;
    const xLabels = n ? [buckets[0].label, buckets[Math.floor(n / 2)].label, buckets[n - 1].label].map(l => `<span>${esc(l)}</span>`).join('') : '';
    // Whole-number ticks only: with a maximum of 1 there is no whole-number midpoint.
    const mid = max > 1 ? String(Math.round(max / 2)) : '';
    const yLabels = [String(max), mid, '0'].map(v => `<span>${v}</span>`).join('');
    const legend = `<div class="chart-legend">${keys.map(k => `<span><i style="background:${k.color}"></i><em>${esc(k.name)}</em> <b>${buckets.reduce((s, b) => s + (b.values[k.key] || 0), 0)}</b></span>`).join('')}</div>`;
    return `<div class="chart-box" style="--chart-h:${height}px">
        <div class="chart-y">${yLabels}</div>
        <svg class="chart" viewBox="0 0 ${n * 10} 100" preserveAspectRatio="none" role="img" aria-label="Bar chart">
            <line x1="0" x2="${n * 10}" y1="100" y2="100" class="grid" vector-effect="non-scaling-stroke"/>${bars}</svg>
        <div class="chart-x">${xLabels}</div>
    </div>${legend}`;
}
