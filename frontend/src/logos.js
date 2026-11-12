// Logos for each kind of connection. See assets/connections/README.md for
// where each file comes from and the trademark notice.
import docker from './assets/connections/docker.svg';
import kubernetes from './assets/connections/kubernetes.svg';
import nomad from './assets/connections/nomad.svg';
import prometheus from './assets/connections/prometheus.svg';
import loki from './assets/connections/loki.svg';
import ssh from './assets/connections/ssh.svg';

const LOGOS = {docker, kubernetes, nomad, prometheus, loki, ssh};

// logo returns an <img> for a connection type at the given pixel size (the
// image is decorative: the type's name is always shown next to it).
export function logo(type, size = 24) {
    const src = LOGOS[type];
    if (!src) return `<span class="tlogo-missing" style="font-size:${Math.round(size * 0.8)}px" aria-hidden="true">?</span>`;
    return `<img class="tlogo" src="${src}" alt="" width="${size}" height="${size}" draggable="false">`;
}
