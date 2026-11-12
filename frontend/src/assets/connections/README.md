# Connection logos

Icons shown for each kind of connection (sidebar, connection cards, dashboards,
Overview, Add connection dialog).

| File | Source | License of the artwork file |
| :--- | :--- | :--- |
| `docker.svg`, `kubernetes.svg`, `nomad.svg`, `prometheus.svg` | [Simple Icons](https://github.com/simple-icons/simple-icons) **v16.37.0**, `icons/<name>.svg` | CC0 1.0 (public domain dedication) |
| `loki.svg`, `ssh.svg` | Drawn for PhantomOps (generic "log lines" and "terminal" glyphs) | Apache-2.0, like the rest of this repository |

The Simple Icons files are unchanged except for one added `fill="#RRGGBB"`
attribute on the `<svg>` element, set to the brand colour published in Simple
Icons' `data/simple-icons.json` (Docker `#2496ED`, Kubernetes `#326CE5`,
Nomad `#00CA8E`, Prometheus `#E6522C`), so they work as plain `<img>` files.

## Trademarks

Docker is a trademark of Docker, Inc. Kubernetes is a trademark of The Linux
Foundation. Nomad is a trademark of HashiCorp, Inc. Prometheus is a trademark
of The Linux Foundation. Loki and Grafana are trademarks of Grafana Labs.
OpenSSH is a project of the OpenBSD Project. The logos above are used only to
identify the services PhantomOps can connect to; PhantomOps is not affiliated
with, endorsed by or sponsored by any of them.

Loki and SSH use generic glyphs on purpose: Grafana publishes Loki's logo only
inside the AGPL-3.0 `grafana/loki` repository, and SSH has no freely reusable
brand logo. To use the official Loki logo, replace `loki.svg` with a version you
have permission to use.
