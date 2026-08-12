# SMBARK landing page

Static HTML/CSS/JS landing page for **SMBARK**, using the real TUI screenshots as the hero carousel.

## Preview

No build step:

```bash
cd smbark-site
python -m http.server 8080
```

Then open `http://localhost:8080`.

## Before publishing

Two pieces are intentionally placeholders because the final repository/package locations were not provided:

1. Set `REPO_URL` near the top of `js/app.js`.
2. Replace the install commands in the `installCommands` object in `js/app.js` with the real package commands.

## Structure

```text
smbark-site/
├── index.html
├── css/
│   └── styles.css
├── js/
│   └── app.js
└── assets/
    ├── favicon.svg
    ├── smbark-wordmark.png
    └── screenshots/
        ├── discover.webp
        ├── mounted.webp
        ├── automount.webp
        └── config.webp
```

The site has no runtime dependencies, package manager, framework, web fonts, analytics, or third-party JS.
