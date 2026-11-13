# UI screenshots

`shot.py` renders the real Vite build with a fake Wails backend and headless Firefox. It never opens a window or contacts infrastructure.

```sh
python3 dev/ui/shot.py
python3 dev/ui/shot.py settings dashboard --expand
python3 dev/ui/shot.py --size 900x700 --out /tmp/phantomops-small
python3 dev/ui/shot.py --clean
```

The first run caches Playwright and Firefox under `/tmp/phantomops-ui` (or `$PHANTOM_UI_HOME`). The generated PNGs go to `/tmp/phantomops-shots` by default and are not project files.
