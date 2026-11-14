# mtools-plugin-pdf

PDF Utility sidecar plugin for **mtools** (`com.mtools.pdf`).

## Build & Package

```sh
go build -o dist/bin/tool .
mtools-plugin pack \
  -manifest manifest.json \
  -surfaces surfaces.json \
  -bin dist/bin/tool \
  -key /path/to/key.priv \
  -out dist/com.mtools.pdf.zip
mtools-plugin verify -bundle dist/com.mtools.pdf.zip
```
