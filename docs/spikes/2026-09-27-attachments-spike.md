# Spike: paste / drag-and-drop / 📎 attachments in spk-mm-client (WebKitGTK 2.52.3, Wails v3.0.0-beta.25)

Date: 2026-09-27. Investigation only: nothing was committed or built to `build/bin`.
Everything ran headless under `xvfb-run -a` (private Xvfb display, `TMPDIR=.agents/tmp`). The user's
display, clipboard and running client were never touched.

Scratch harnesses and logs: `/home/spk/.spk/sawe/ss/Mattermost/.agents/tmp/paste-spike.QnXc/`
(`harness.py`, `owner.py`, `dnd.py`, `scheme_upload.c`, `chromium.mjs`, `run1..6.log`, `dnd-*.log`,
`scheme.log`, plus the WebKit, Wails and Mattermost source excerpts I read).

---

## 0. Verdict in one paragraph

On WebKitGTK the page **never gets pasted or dropped file contents**. `clipboardData.files` and
`dataTransfer.files` are always empty, because `DataTransfer::allowsFileAccess()` is hard-coded `false`
on non-Cocoa ports (WebKit bug 271957). A clipboard image shows up as **no types at all**. Files copied in
a file manager show up as the single type `text/uri-list`, and `getData` returns `""` for it. The default
action still inserts the file **paths** as text into the textarea. **Text paste is unaffected**, and it
stays correct if we call `preventDefault` only when `types` includes `text/uri-list` and
`getData('text/uri-list') === ''`.

There are two working ways to get the data:
- **JS**: inside the `paste` handler, `navigator.clipboard.read()` works with Wails' default settings.
  It returns image/png bytes (a 25 MB image in about 250 ms). For files it returns only paths (the
  uri-list holds just the first URI).
- **Go (cgo, GTK3 clipboard on the main thread)**: this reads everything reliably and fast. TARGETS
  take 1–5 ms, URIs about 1 ms, a 25 MB PNG about 136 ms.

For **drag-and-drop** the page cannot get files either. Wails' `EnableFileDrop` intercepts file drags at
the GTK level, so the page sees no drag events at all. Wails then hands us **native paths** through
`WindowFilesDropped` (verified with a real XDND drag on Xvfb). For **📎**, Wails' `Dialog.OpenFile()`
has `PromptForMultipleSelection()`, which returns native paths.

Recommendation: **one Go attachment pipeline fed by native paths wherever possible.** Desktop
paste/drop/📎 give paths, or a PNG that Go reads from the clipboard. In browser mode the page's `File`
bytes are sent as a raw POST body. Go uploads each file with `POST /api/v4/files` (simple raw-body mode)
through the `transfer` client, then creates the post with `file_ids`.

Hard gotcha: a `fetch()` to `wails://` with a **Blob/File/FormData body crashes the whole app**. It is a
SIGSEGV in `webkit_uri_scheme_request_get_http_body`, and Wails calls it for every request. Uint8Array
and ArrayBuffer bodies are fine: 60 MB streams through in about 300 ms.

---

## 1. Environment and method

- System WebKitGTK **2.52.3** (webkit2gtk-4.1, GTK3). This is the same stack as the desktop build:
  Makefile `DESKTOP_TAGS := wails gtk3`, and Wails `linux_cgo_gtk3.go` uses `pkg-config: gtk+-3.0 webkit2gtk-4.1`.
- **WebView settings mirror Wails beta.25.** In `linux_cgo_gtk3.go:1538-1553` Wails only sets the UA app
  details and the GPU policy. It does **not** set `javascript-can-access-clipboard` (default `False`) or
  `enable-developer-extras` (it is changed only if DevTools is on). No sandbox is involved
  (`WebContext.get_sandbox_enabled() == False`).
- The clipboard owner is a separate process (`owner.py`) on the Xvfb display. It uses either
  `Gtk.Clipboard.set_image` (what GTK screenshot tools do) or raw GTK selection targets:
  - "gtk3" file-manager style (Nautilus-3/Nemo): `x-special/gnome-copied-files`, `text/uri-list`,
    `UTF8_STRING`, `text/plain` (paths).
  - "gtk4" Nautilus style: text is `file://` URIs.
  - "nemo-min" worst case: no uri-list.
  - `image/png` only (Qt/flameshot-style raw PNG).
  - A 3840×2160 noise PNG of **24,929,489 bytes**.
- Two paste triggers, which give identical results:
  1. `webview.execute_editing_command("Paste")`.
  2. A real **Ctrl+V** through XTest on the Xvfb display only (python-xlib; the harness asserts
     `DISPLAY` is not `:0`).
- Drag-and-drop: `dnd.py` runs a GTK drag-source window offering `x-special/gnome-copied-files` +
  `text/uri-list` + text (a file-manager stand-in) and a WebView window. An XTest press → move → release
  performs a real XDND drag. Two modes: plain WebKit, and "wails-enable", which uses the same GTK signal
  handlers as Wails' `enableDND`.
- Browser mode: headed Chromium (Playwright 1.63 from `tests/e2e`) under Xvfb, with the same clipboard
  owner, plus synthetic events.

---

## 2. Evidence

### 2.1 WebKitGTK `paste` event in a focused `<textarea>`, default settings (run1.log; `cmd` and `xtest` identical)

| scenario | `types` | `items` | `files` | `getData` | textarea after default paste |
|---|---|---|---|---|---|
| image via `set_image` (small, 1318 B) | `[]` | `[]` | `[]` | – | `""` |
| raw `image/png` only (681 B) | `[]` | `[]` | `[]` | – | `""` |
| raw `image/png` 25 MB | `[]` | `[]` | `[]` | – | `""` |
| image via `set_image`, 4K | `[]` | `[]` | `[]` | – | `""` |
| 1 file (note.txt), gtk3 style | `["text/uri-list"]` | 1× string `text/uri-list` | `[]` | `text/uri-list` → `""` | the **path** is inserted |
| 2 files | `["text/uri-list"]` | same | `[]` | `""` | both paths inserted (newline-separated) |
| PNG copied as a file | `["text/uri-list"]` | same | `[]` | `""` | path inserted |
| unicode/space name + blob.bin | `["text/uri-list"]` | same | `[]` | `""` | paths inserted |
| gtk4 Nautilus style | `["text/uri-list"]` | same | `[]` | `""` | `file:///…` URI inserted |
| Nemo-min (no uri-list) | `[]` | `[]` | `[]` | – | path inserted (from UTF8_STRING) |
| uri-list only | `["text/uri-list"]` | same | `[]` | `""` | `""` |
| plain text | `["text/plain"]` | string | `[]` | `"plain hello"` | `"plain hello"` |
| `text/html` + UTF8_STRING | `["text/html"]` | string | `[]` | sanitized HTML | `"bold x"` |

- `isTrusted: true` in every case.
- The event arrives within about 3–11 ms of handler work (`syncMs`). Wall time from trigger to report
  is about 255–305 ms, which is dominated by the 250 ms settle timer in the harness.
- The clipboard owner log shows WebKit **never even requested `image/png`**. For file lists it requested
  `text/uri-list` 5× plus UTF8_STRING once.

**Root cause (WebKit source, tag `webkitgtk-2.52.3`).** `Source/WebCore/dom/DataTransfer.h`:

```cpp
bool allowsFileAccess() const {
#if PLATFORM(COCOA)
    return !forDrag() || forFileDrag();
#else
    // Check https://webkit.org/b/271957 before allowing file access for your port.
    return false;
#endif
}
```

- `DataTransfer::types()` hides `"Files"`: `hideFilesType = !canWriteData() && !allowsFileAccess()`.
  For file paths only `text/uri-list` is added, and `getData` is suppressed to avoid exposing file
  paths.
- `filesFromPasteboardAndItemList()` reads files only when `allowsFileAccess()` is true.
- So the GTK platform code (`PasteboardGLib.cpp` `read(PasteboardFileReader&)`, which knows how to read
  file paths and `image/png` buffers) is **dead code for the DOM**. This applies to both paste **and
  drop**, and no WebKitGTK setting changes it.

### 2.2 Async Clipboard API inside the paste handler (run2–run4.log)

`navigator.clipboard.read()` was called from the paste handler, including after `await`s:

| scenario | default Wails settings | `javascript-can-access-clipboard=TRUE` |
|---|---|---|
| image (`set_image`) | `[{type:"image/png", size:1318, head:"89 50 4e 47 …"}]`, 3 ms | same |
| raw PNG 25 MB | `[{image/png, 24929489}]`, **248–287 ms** | same |
| 2 files | `text/plain` (both paths, `\n`) + `text/uri-list` (**only the first URI**) | same |
| Nemo-min | `[]` | `[]` |
| text | `text/plain` | same |

Outside a user paste (no gesture), with default settings the call fails with
`NotAllowedError: The request is not allowed by the user agent…`. With
`javascript-can-access-clipboard=TRUE` it works anywhere, but Wails does not expose that setting. The
blob bytes are the original PNG, unchanged in size.

### 2.3 Go side: GTK3 clipboard read on the UI main thread (run2.log, `PROBE=1`)

These are the same calls Go would make through cgo. They ran on the main thread, immediately before the
paste.

```
img-gtk-small : targets=[TIMESTAMP,TARGETS,MULTIPLE,SAVE_TARGETS,image/png,image/jpeg,image/bmp,…,image/tiff,image/webp] 4.6 ms; uris=[] 0.6 ms; image/png 1318 B (89504e47…) 2.3 ms
img-raw 25 MB : targets=[…,image/png] 3.5 ms; wait_for_contents(image/png) 24,929,489 B in 136 ms
files-two     : targets=[…,x-special/gnome-copied-files,text/uri-list,UTF8_STRING,text/plain] 2.0 ms; wait_for_uris → both file:// URIs, 1.0 ms
files-nemo-min: targets=[…,x-special/gnome-copied-files,UTF8_STRING] (uris=[]; parse gnome-copied-files instead)
text          : targets=[…,UTF8_STRING,text/plain]
async request_contents(TARGETS) callback: 0.3–5.7 ms
```

### 2.4 `preventDefault` only for file lists (run4, run5.log)

The rule is `types.includes('text/uri-list') && getData('text/uri-list') === ''`.

| scenario | types | prevented | textarea |
|---|---|---|---|
| files | `["text/uri-list"]`, getData `""` | yes | `""` (no paths inserted) |
| http link (uri-list + text/plain) | `["text/uri-list","text/plain"]`, getData `"https://example.com/a\r\n"` | no | `"https://example.com/a"` |
| text/html + text | `["text/html"]` | no | `"bold x"` |
| plain text | `["text/plain"]` | no | `"plain hello"` |

A rejected alternative, "prevent when `text/plain` is missing", wrongly swallowed the html-only paste
(run4).

### 2.5 Real external file drag onto the WebView (dnd.py, XDND on Xvfb)

- **Plain WebKit.** `dragenter` has types `["text/uri-list","text/html"]`. `drop` has `files: []` and
  `getData('text/uri-list') === ""`. The `text/html` value contains `<a …>file:///…/note.txt</a>`, so
  **only the first path leaks, and only via html**.
- **Wails-like handlers** (`drag-motion`/`drag-drop` return TRUE for uri-list drags;
  `drag-data-received` reads the data). The page gets **no DOM drag events at all**, and native
  `drag-data-received` fires with `info=2`:
  `uris=['file:///…/note.txt','file:///…/shot.png']`. This confirms Wails' `target_type != 2` check
  matches WebKit 2.52's target list.

### 2.6 Custom-scheme POST (what `fetch('/…')` on `wails://localhost` goes through) — scheme_upload.c / scheme.log

The test page was served from a custom scheme `app://`. It POSTed to its own origin, and the UI process
read the body with `webkit_uri_scheme_request_get_http_body()` (exactly what Wails does).

```
u8 (Uint8Array) : 1 MB 7 ms | 25 MB 163 ms | 60 MB 297 ms   (stream: WebKitFormDataInputStream, 1 MiB reads)
ab (ArrayBuffer): 1 MB 6 ms | 25 MB 154 ms | 60 MB 303 ms
blob / File     : Segmentation fault — gdb: #4 webkit_uri_scheme_request_get_http_body () ← handler
```

The PyGObject variant also segfaulted on the Blob body.

Wails calls `r.Body()` **unconditionally** for every scheme request (`internal/assetserver/assetserver_webview.go:157`
→ `webview/webkit_linux_gtk3.go:105`). Therefore any `fetch(wails://…, {body: Blob | File | FormData(File)})`
would crash the desktop app. Convert to `new Uint8Array(await file.arrayBuffer())` first.

### 2.7 Chromium (browser mode), real Ctrl+V with the X clipboard (chromium.mjs)

```
img-gtk        types=["Files"] items=[file image/png] files=[image.png 1318]   bytes read 1318   textarea ""
img-rawpng-4k  files=[image.png 24929489] read 24929489
files-two      types=["Files"] files=[note.txt 16 text/plain, shot.png 681 image/png] (real contents!)
               but the default action ALSO inserts the paths into the textarea → preventDefault when files.length>0
text           types=["text/plain"] → "plain hello"
synthetic      new ClipboardEvent('paste', {clipboardData: dt}) with dt.items.add(new File(...)) → handler sees
               types ["Files"], the File and its bytes (isTrusted:false). new DragEvent('drop', {dataTransfer: dt}) → same.
```

In browser mode the page **does** get real files for paste (and for drop). For e2e, dispatch synthetic
events built from a `DataTransfer`, and use `page.setInputFiles()` for an `<input type=file>`.

---

## 3. Verdicts

**(a) Clipboard image → `paste` event.** **No.**
- `clipboardData.files` and `items` are empty and `types` is `[]`, for small and 25 MB images and for
  both `set_image` and raw `image/png`. WebKit does not even fetch the PNG. There is no size effect; it
  is a policy block (`allowsFileAccess() == false`).
- Readable bytes are available two other ways:
  - **In JS** via `navigator.clipboard.read()` inside the paste handler (image/png Blob, full size;
    25 MB in about 250 ms).
  - **In Go** via GTK (about 136 ms for 25 MB).
- No DOM-exposed size limit was observed. X INCR transfers of 25 MB work.

**(b) Files copied in a file manager → `paste` event.**
- No file contents. The page sees only `types: ["text/uri-list"]` with `getData` returning `""`. The
  default action inserts the **paths as text** into the textarea, which must be suppressed.
- One file, two files, a PNG copied as a file, unicode/space names and the gtk4 URI text all behave the
  same.
- The Nemo-min variant (no uri-list) shows `types: []`, and the default action still inserts the paths.
- `navigator.clipboard.read()` exposes paths only: `text/plain` (whatever the file manager put there)
  and a `text/uri-list` truncated to the first URI.

**(c) Go reading the clipboard.** **Feasible, fast, and the recommended source for pasted files. Also
good for images.**
- Wails beta.25 has only a **text** clipboard API (`clipboard_linux.go`; `gtk_clipboard_wait_for_text`
  under `InvokeSync`), so we need our own cgo. Put it in a file behind `//go:build linux && cgo && wails && gtk3`
  with a no-op stub, so plain `go build ./...` still passes.
- The API: `gtk_clipboard_get(GDK_SELECTION_CLIPBOARD)` →
  - `wait_for_targets`;
  - `wait_for_uris`, or parse `x-special/gnome-copied-files` (`copy\nfile:///…`) as a fallback for
    owners without uri-list;
  - `wait_for_contents("image/png")` (fall back to `wait_for_image` + `gdk_pixbuf_save_to_buffer("png")`
    if only jpeg/bmp are offered).
- Threading: GTK must be called on the main thread. Bound methods run on goroutines, so hop over with
  `application.InvokeSync`, as Wails' own `Clipboard.Text()` does. The `wait_*` calls spin a nested main
  loop, so the UI stays painted, and they took 1–5 ms for targets/URIs and 136 ms for 25 MB.
- Better: use `gtk_clipboard_request_contents` via `InvokeAsync` with a Go channel and a Go-side
  timeout (for example 3 s). Then a hung clipboard owner cannot freeze the UI at all. Callback latency
  was 0.3–6 ms.
- Copy the bytes out on the main thread, then write them to a spool file off-thread.

**(d) Text paste when `preventDefault` only covers file/image content.** **Yes, it behaves.**
- Plain text, http links (uri-list + text/plain) and html all paste normally.
- Only file lists are prevented, detected by `text/uri-list` with an empty `getData`.
- Images need no `preventDefault` (the default inserts nothing), but the handler should still ask Go or
  `clipboard.read()`.
- Chromium: prevent when `clipboardData.files.length > 0`.

**Drag-and-drop.** The page `DataTransfer` path is **dead on WebKitGTK** (same `allowsFileAccess`
block; only the first path leaks via `text/html`). Use **Wails native paths**:
- Set `WebviewWindowOptions.EnableFileDrop: true`. Wails connects `drag-motion`/`drag-drop`/
  `drag-data-received` on the WebView (`linux_cgo_gtk3.go:283-450`) and returns TRUE for any drag
  carrying `text/uri-list`, so WebKit (and the page) never sees those drags.
- Data flow:
  1. The native side converts URIs with `g_filename_from_uri`, so unicode and spaces are fine.
  2. It calls JS `window._wails.handlePlatformFileDrop(paths, x, y)`.
  3. The runtime (`@wailsio/runtime` `window.js` `HandlePlatformFileDrop`) finds the element under the
     point that has `data-file-drop-target`. If there is none, the drop is **silently ignored**.
  4. The runtime calls back to Go (`WindowFilesDropped`, method 50).
  5. Go fires `events.Common.WindowFilesDropped` listeners with `ctx.DroppedFiles()` and
     `ctx.DropTargetDetails()` (`id`, `classList`, `attributes`).
- Hover feedback: the runtime toggles the class `file-drop-target-active` on the target while hovering
  (`handleDragEnter`/`handleDragOver`, throttled at 5 px).
- The app currently has `EnableFileDrop` unset. `disableDND` therefore blocks every external file drop:
  the page gets nothing and there is no navigation.
- Internal HTML5 drags (none in the app today) are left to WebKit.
- Browser mode: use a plain DOM `drop` handler; Chromium gives real `File`s.

**📎 button.** `app.Dialog.OpenFile()` (`dialog_manager.go:16`) supports `.CanChooseFiles(true)`,
`.AttachToWindow(w)`, `.SetTitle`, `.AddFilter`, and **`.PromptForMultipleSelection() ([]string, error)`**,
which returns absolute paths.
- Implementation: a `GtkFileChooserDialog` (not the XDG portal) run with `gtk_dialog_run` on the GTK
  main thread via `InvokeAsync`. The calling goroutine blocks on a channel until the user closes it.
- Rules:
  - Call it from a bound-method goroutine, **never from the main thread** (it would deadlock waiting
    for itself).
  - Attach it to the window to make it modal.
  - The UI stays responsive (nested loop).
  - With the D-Bus session bus cut off (`busprobe_linux.go` points `DBUS_SESSION_BUS_ADDRESS` at a
    dead address), GtkFileChooser still browses local files. Recent/GVfs bits may warn but fail fast.
- Browser mode: `<input type="file" multiple>` → `File`s → the same byte upload path. In e2e, use
  `setInputFiles`.

---

## 4. Recommended architecture

### 4.1 One attachment model, two ingest kinds, one Go upload pipeline

```
            desktop (Wails)                                 browser mode (Chromium)
Ctrl+V ──► paste handler ─► Go: AttachFromClipboard(srv, ch)   paste handler ─► File[] (clipboardData.files)
DnD    ──► Wails EnableFileDrop ─► Go WindowFilesDropped      DOM drop ─► File[]
📎     ──► Go: PickAttachments(srv, ch) → Dialog.OpenFile     <input type=file multiple> ─► File[]
                 │ native paths / clipboard PNG                     │ bytes
                 ▼                                                  ▼
        attach.Store.AddPath(srv,ch,path)             POST /api/attachments?srv&ch&name&mime (raw body)
        attach.Store.AddBytes(srv,ch,name,mime,r)  ◄──┘  (HTTP transport; in desktop only if ever needed,
                 │                                      as Uint8Array — never Blob — over wails://)
                 ▼
     staged attachment {id, srv, ch, name, mime, size, source(path|spool), state, fileID?, err?}
                 │  upload worker (per server; via transfer client; while StatusLive)
                 ▼
     POST /api/v4/files?channel_id=…&filename=…&client_id=<attachment id>   (raw body, Content-Length)
                 │  201 {file_infos:[FileInfo], client_ids:[…]}
                 ▼
     SendPost(srv, ch, message, attachmentIDs) → pending post (shows local files) → waits for uploads
     → POST /api/v4/posts {…, file_ids, pending_post_id}
```

**Ingest.**
- **Paths (desktop):**
  - Stat the path (`os.Stat` → regular file, size ≤ `MaxFileSize`).
  - Keep only the path; stream from it at upload time.
  - Record size/mtime and re-check them at upload. If the file changed or vanished, fail that
    attachment with a clear error.
  - Nothing is copied, and memory stays flat.
- **Clipboard PNG (desktop):**
  - Copy the bytes out of GTK on the main thread and spool them to
    `~/.spk/mm-client/tmp/attach-<id>.png` (unique name like `Screenshot 2026-09-27 22-31-05.png`).
  - Delete the spool file after a successful post, a discard, or at startup (sweep stale spools
    asynchronously; never block startup).
- **Bytes (browser mode):**
  - The page streams the `File` as a **raw request body** to a new HTTP route on the existing loopback
    UI server, protected by the bearer token + `OriginGuard` + `LoopbackHostGuard` like other `/api/`
    calls.
  - Suggested route: `POST /api/attachments/{srv}/{channel}?name=…&mime=…`.
  - Go spools the body to disk with `http.MaxBytesReader(MaxFileSize)`.
  - Do not use JSON/base64. There is no need for multipart either, since there is one file per
    request.

**Identity and UI.**
- UI state holds `attachmentIDs` per composer (per channel). Go emits `attachments_changed` with
  `{srv, ch, items: [{id, name, size, mime, state: staged|uploading|uploaded|failed, progress, error}]}`,
  coalesced like the other events.
- Thumbnails for staged images come from Go via the existing media route shape:
  `/media/<srv>/staged/<attachmentID>` (desktop via `withMedia` on `wails://`, browser via the
  cookie-guarded `/media/`). Reuse the raster-only/size/pixel guards. In browser mode the page could use
  `URL.createObjectURL(file)` instead, but one server-side route keeps the two modes identical.

**Upload timing.**
- Start uploading **eagerly** as soon as something is staged and the worker is `StatusLive`, as the
  official webapp does. Uploads are channel-bound on the server (`channel_id` query param), so staging
  must stay per channel.
- **Send** creates the pending post immediately (optimistic; the feed shows the files from the staged
  list), then waits for that post's uploads and calls `CreatePost` with `file_ids`.
- Offline: attachments stay `staged` and the upload runs when the worker is live again.
- If an upload fails, the pending post fails. **Retry** re-uploads only the failed ones: attachments
  that already have `fileID` keep it, because the server lets them be attached later as long as the
  same user and channel own them.
- **Discard** drops the pending post and its attachments (and spools). Already-uploaded orphan
  `FileInfo`s stay on the server unattached, which is harmless; the webapp leaves them too.

**Uploader.**
- Uses `Service.transfer` (no whole-request timeout; internal/api/service.go:28,74) plus a
  progress-stall timeout (for example "no bytes for 60 s").
- Uses the shared per-server rate limiter.
- Concurrency 2 per server.
- Progress via a counting reader, throttled to about 4 Hz, like downloads.
- **Not retried automatically on transport errors.** This follows the rule that POSTs are not repeated
  on network errors. A duplicate upload would only orphan a file, so user-driven Retry is enough.
- 401 → `signalAuth()` like other calls.

**Limits.**
- Read `MaxFileSize` and `EnableFileAttachments` from `/api/v4/config/client?format=old`. They are
  present in the full client config for logged-in users: server/config/client.go:75,86. Extend
  `rest.ClientConfig` (internal/mm/rest/endpoints.go:12) and `state.Config` (mmsync/worker.go:698).
- Refuse to stage when `EnableFileAttachments` is false or the size is over `MaxFileSize` (server
  default 100 MiB).
- Cap attachments at **10 per post** (webapp `MAX_UPLOAD_FILES: 10`; the server's
  `PostFileidsMaxRunes = 300` on the JSON array of 26-char ids allows about 10).

### 4.2 Desktop paste handler (Composer)

```ts
onPaste = (e) => {
  const cd = e.clipboardData
  if (isDesktop()) {
    const fileList = cd.types.includes('text/uri-list') && cd.getData('text/uri-list') === ''
    if (fileList) e.preventDefault()                    // else WebKit inserts the paths as text
    if (fileList || !cd.types.includes('text/plain')) // file list, bare image ([]), html(+image)
      void attachFromClipboard(srv, ch)                  // Go reads targets → uris / gnome-copied-files / image/png
  } else if (cd.files.length > 0) {                      // Chromium: real Files
    e.preventDefault(); void uploadFiles(srv, ch, [...cd.files])
  }
}
```

- `AttachFromClipboard` in Go does these in order:
  1. Read TARGETS.
  2. File list: use `uris` (`file://` only) or `x-special/gnome-copied-files` → `AddPath` each.
  3. Otherwise, if `image/png` (or another `image/*`) is offered → spool, then `AddPath`.
  4. Otherwise return "nothing".
- It returns quickly, and the staged items arrive through the event.
- If the handler did not `preventDefault` (html + image) and Go finds an image, both the text and the
  image are kept, which matches the webapp.

### 4.3 Drag-and-drop and 📎 wiring

- **Drop.**
  - Set `EnableFileDrop: true` in `internal/desktop/run.go:184`.
  - Mark the composer region (or the whole channel pane) with
    `data-file-drop-target data-srv=<id> data-channel=<id>`. Style `.file-drop-target-active`.
  - In Go:
    `w.OnWindowEvent(events.Common.WindowFilesDropped, e => { d := e.Context().DropTargetDetails(); paths := e.Context().DroppedFiles(); … AddPath(d.Attributes["data-srv"], d.Attributes["data-channel"], p) })`.
  - Browser mode: a DOM `dragover`/`drop` handler on the same element → `File[]` → byte upload.
- **📎.** Add a bound method `PickAttachments(srv, ch)`:
  - Build the dialog with `app.Dialog.OpenFile().CanChooseFiles(true).AttachToWindow(win).PromptForMultipleSelection()`
    and pass each result to `AddPath`.
  - Needs a seam: the window/app handle lives in `internal/desktop`, not in `api.Service`. Inject a
    `FilePicker` interface into the Service (desktop implements it with Wails; browser/tests return
    "unsupported" or a recording fake).
  - Browser mode: hidden `<input type=file multiple>` → byte upload.

### 4.4 Alternatives considered

| option | verdict |
|---|---|
| Page `clipboardData.files` / `dataTransfer.files` on WebKitGTK | **Impossible** (`allowsFileAccess()==false` on non-Cocoa). |
| `navigator.clipboard.read()` for images → bytes → Go | Works with default settings inside paste (verified). Pros: no cgo for images. Cons: bytes go through the web process (25 MB image → +25–50 MB transient in WebKitWebProcess); must POST as **Uint8Array** (Blob crashes); still needs cgo for files. A reasonable fallback if cgo is unwanted for images only. |
| `navigator.clipboard.read()` `text/plain` paths → Go reads them | Rejected. Paths are **page-supplied**: an XSS could make Go read `~/.ssh/id_rsa`. uri-list holds only the first URI, and text/plain depends on the file manager (Nemo-min gives nothing). |
| Bytes via Wails binding (base64 in JSON) | Rejected. The runtime body limit is 64 MiB assembled (`transport_http.go:39-41`, 512 KiB chunks), base64 adds ×4/3, and the JSON string + decode is roughly ×3 transient Go heap against a 64 MiB soft `GOMEMLIMIT`. |
| Raw POST to the loopback **media** server (`internal/media.Loopback`) | Rejected. It is GET/HEAD-only by design with no CORS. Cross-origin from `wails://` would need CORS and preflight, which weakens its guard. |
| Raw POST to `wails://localhost/<route>` (asset handler, `withMedia`) | Works for Uint8Array/ArrayBuffer (60 MB/300 ms, streamed), but **Blob/File/FormData bodies SIGSEGV the app**. Keep it only as the "JS image bytes" fallback, with a hard rule plus a test. |
| Upload sessions `POST /api/v4/uploads` + `POST /uploads/{id}` (resumable) | Not needed now (≤100 MiB default; simple POST streams from disk). Consider later for flaky networks or big files. |
| Multipart `POST /api/v4/files` | Works, but `channel_id` must precede the files or the server falls back to a buffered legacy mode (`api4/file.go:198-285`). Simple mode (`?channel_id&filename&client_id`, raw body, `Content-Length` required, `api4/file.go:98,133-176`) is simpler for one file per request. |
| Own GTK drop handlers instead of Wails `EnableFileDrop` | More cgo for little gain. Wails' path works (verified with the same handler shape). See the risk below about forged `FilesDropped`. |

---

## 5. Context survey (read-only)

**Composer**
- `frontend/src/components/Composer.tsx` (93 lines) is a plain `<textarea>`.
- Props: `onSend(message)`, `onDraft`, `onEditLast`.
- Enter sends; Shift+Enter adds a newline; ArrowUp in an empty box edits the last own post.
- The draft is debounced 500 ms (`DRAFT_DELAY`) and flushed on unmount.
- `send()` clears the box. If `onSend` throws, it puts the text back, re-saves the draft and shows
  `role=alert`.
- It is keyed by channel (`ChannelPane.tsx:103-109`).
- Tests: `Composer.test.tsx` has 5 tests (send/newline/blank, refused send restores, restored text
  persisted as draft, ArrowUp, draft timing).
- There is no paste handling today, and no attachment UI.

**Send path**
1. `ChannelPane` → `chat.ts:141` `sendPost` → `client.sendPost`.
2. The transport is either Wails `Call.ByName('…transport.API.SendPost', id, ch, msg)`
   (`api/client.ts:125-161`) or HTTP `POST /api/SendPost` (`client.ts:93`).
3. `transport/wails.go` / `transport/http.go` → `api.Service.SendPost` (`internal/api/chat.go:115`).
   It uses `s.writer()`, which fails fast with `session_expired` on `needs_reauth`.
4. `mmsync.Worker.Send` (`mmsync/actions.go:83-91`): `st.AddPending` → `changed` → `startCreate` on a
   background goroutine (`goBG`). If the worker is stopping, the post fails immediately.
5. `create()` (`actions.go:93-107`) calls `rc.CreatePost` with `PendingPostID` under
   `createTimeout = 30 s` (`worker.go:113`).
   - On error: `FailPending` (+ `signalAuth` on an expired session).
   - On success: `PostCreated`.
   - The WS echo carries the same `pending_post_id`.
6. Retry (`actions.go:109-119`) resends with the same pending id; the server dedups for 30 s. Discard
   calls `DropPending`.
7. Pending posts live **only in memory** (`state.Server.pending`, `state/posts.go:330-381`). There is no
   SQLite outbox, so they are lost on restart.
8. In `state/view.go:98-107` pending posts are appended after real posts as
   `PostView{Pending: !Failed, Failed, PendingPostID}`. `Files` is empty for pending posts today, so
   pending attachments need a local `FileView` plus a staged preview URL.
9. Offline: `Send` still stages and attempts `CreatePost`, which fails as a network error, so the post
   shows as failed with Retry. There is no queued send.

**Pending rendering**
- `PostItem.tsx:158` shows pending posts at `opacity-60` with "Sending…" (`:199`).
- Failed posts show Retry/Discard (`:200-206`).
- The feed row key survives confirmation via `pending_post_id` (`feedRows`, `Feed.tsx:231,237`).
- Attachments render through `Attachments.tsx` / `FileCard.tsx` from `post.files`.

**Transport**
- Desktop: Wails bindings over `POST wails://localhost/wails/runtime` with JSON; bodies over 512 KiB are
  chunked, with a 64 MiB total cap.
- Events: `app.Event.Emit(type, payload)` from the Emitter (`desktop/run.go:163-179`), subscribed by
  `EVENT_TYPES` in `client.ts:135-143`. Add `attachments_changed` there.
- The asset handler is `withMedia(assets, media)` (`desktop/media.go:8`), so extra routes could be added
  on `wails://`.
- Browser: `transport.HTTP` (`/api/<Method>` JSON, bearer token from the `<meta>` tag, SSE events),
  plus `OriginGuard` (mutations need a same-host Origin/Referer) and `LoopbackHostGuard`.
  `/media/` sits behind the cookie/bearer `mediaGuard` (`cmd/spk-mm-client/browser.go:138,160-300`).
- The `handle[Req]` helper JSON-decodes the body, so a raw-byte upload route needs its own handler.

**internal/mm/rest**
- `Client.do` (`rest/client.go:99`): JSON only, `Authorization: Bearer`, optional
  `rate.Limiter(10/s, burst 20)`.
- Retries: GET only (up to 5 attempts with backoff). 429 is honoured via `Retry-After`.
- Classification: 401/403 → `KindAuth`, 5xx → `KindNetwork`.
- The default `http.Client` has `Timeout: 30 s`. Long transfers use `WithHTTPClient(s.transfer)` plus
  `Stream` (`rest/media.go`).
- **There is no upload code anywhere.** `CreatePost` (`rest/write.go:24`) sends only
  `channel_id, message, root_id, pending_post_id, user_id`, so add `file_ids`.
- `ClientConfig` lacks `MaxFileSize` and `EnableFileAttachments`.

**internal/mmfake**
- Files are `chat.files map[string]*ffile{info FileInfo, channelID, data []byte, thumb, preview}`
  (`mmfake/media.go:26-33`).
- `newFileLocked` derives thumbnail/preview/dimensions like the server (`media.go:143-161`).
- Only GET routes exist: `/files/{id}`, `/thumbnail`, `/preview`, `/info` (`media.go:212-215`).
- `createPostLocked` copies `FileIDs` and fills `metadata.files` **without validation**
  (`chat.go:396-399`).
- Needed:
  - `POST /api/v4/files`: simple mode (`channel_id`, `filename`, `client_id`, raw body) and ideally
    multipart too. Check membership/permission, `EnableFileAttachments` and `MaxFileSize` (413), and
    return 201 `{file_infos, client_ids}`. Record `user_id` and `post_id=""`.
  - Validation in `createPostLocked` that mirrors `attachFileIDsToPost`: silently drop ids that are
    unknown, from another channel or user, or already attached, then set `post_id`.
  - `MaxFileSize` and `EnableFileAttachments` in `clientConfig` (`server.go:183`).
  - Test-API knobs: fail/slow uploads (like `throttle-file`) for e2e of progress/failure/retry.

**API facts doc**
- `docs/research/2026-09-24-mattermost-api-facts.md` §7.3 covers **downloads only** (routes, thumbnail
  120×100, preview ≤1920, Range, FileInfo fields, `has_preview_image`). §3 has `POST /posts`
  idempotency via `pending_post_id` (30 s).
- Nothing about uploads. Upload facts to add, from `release-10.11` source:
  - `POST /api/v4/files` → **201** `FileUploadResponse{file_infos:[FileInfo], client_ids:[…]}`.
  - 403 `api.file.attachments.disabled.app_error` when attachments are disabled.
  - `Content-Length` 0 → 400.
  - Simple mode needs the `channel_id` and `filename` query params plus an optional `client_id`
    (`api4/file.go:77-176`).
  - Multipart streaming needs `channel_id` before the files; `client_ids` must match the file count.
  - `PermissionUploadFile` on the channel.
  - `MaxFileSize` default 100 MiB (`model/config.go:1820`); over-limit uploads get 413.
  - `MaxImageResolution` default 7680×4320.
  - Post `file_ids`: at most 300 runes of JSON (about 10 ids, `model/post.go:57,518`); duplicates are
    removed.
  - Attach: ids that cannot be attached are silently dropped and the post is overwritten with the rest
    (`app/post.go:476-503`).
  - Upload sessions: `POST /api/v4/uploads`, `POST /api/v4/uploads/{id}` (`api4/upload.go`).

**AGENTS.md rules that constrain the design**
- The UI never talks to the MM server. Media only via `/media/<srv>/<kind>/<key>` with checked keys and
  no open proxy. Staged previews must follow that (new `kind`, key = attachment id).
- The token never leaves Go. Uploads happen in Go.
- No blocking syscalls at startup: the spool sweep runs async; the clipboard/dialog calls happen only
  on user action.
- Browser mode is loopback-Host only, with an Origin guard and a bearer token. Put the upload route
  under `/api/`.
- `Emitter.Emit` never blocks.
- Events are coalesced at 100 ms. Progress events are throttled at about 4 Hz, as for downloads.
- Write actions under `needs_reauth` fail fast (`s.writer()`).
- **POSTs are not repeated on network errors.** The only exception is reactions. Upload retry must
  therefore be user-driven, unless a new documented exception is agreed.
- `mmsync.Worker` hooks must not block. Uploads run on `goBG` goroutines.
- Memory:
  - `GOGC=50`, soft 64 MiB. Stream from disk; never hold a whole file in the Go heap.
  - The WebKit web process is also watched. Avoid pushing big bytes through JS on desktop.
- `go build ./...` without `wails` must pass. The GTK cgo goes behind tags with a stub.
- `@wailsio/runtime` and `wails/v3` versions must match.
- D-Bus rules: the file dialog must be launched off service goroutines only by user action, never at
  startup.
- Playwright contexts must be closed.
- Never raise Wails `LogLevel` to Debug. Binding results (paths) would be logged. They are not secret,
  but the same care applies.

---

## 6. Risks and gotchas

1. **Blob/File/FormData `fetch` body to `wails://` → app SIGSEGV** (WebKitGTK 2.52.3 +
   Wails reading `Body()` eagerly). Add this to AGENTS "Things that bite". If JS ever posts bytes on
   desktop, use `Uint8Array`, and add a test that the desktop upload helper never passes a Blob.
2. **Forged drops.** Wails round-trips the dropped paths **through JS**
   (`handlePlatformFileDrop` → `FilesDropped` call). Page script (for example an XSS through markdown)
   could therefore call `WindowFilesDropped` with arbitrary paths and stage `~/.ssh/id_rsa`.
   Mitigations:
   - Staged files are visible chips that the user must send explicitly.
   - Regular files only. Consider refusing dot-directories.
   - Or remember the last native drop time and reject `FilesDropped` without a preceding native
     `drag-drop` (Wails gives no pre-JS hook; it would need an own cgo `drag-drop` observer).
   - `AttachFromClipboard` and the dialog are safe: Go reads the paths itself.
3. **GTK clipboard calls must run on the main thread.** The `wait_*` calls spin a nested loop, so there
   is re-entrancy: other Wails callbacks can run in the middle. Prefer async `request_contents` plus a
   Go timeout. A hung clipboard owner then cannot block anything. GTK's own selection timeout is
   seconds long.
4. **Wayland.** GTK3's clipboard works on Wayland while the window has focus, which is true during
   Ctrl+V. The XDND evidence here is X11 (Xvfb). On Wayland GTK3 uses `wl_data_device`, and Wails'
   handlers are toolkit-level, so they should behave the same. This is unverified here.
5. **Non-PNG clipboard images** (jpeg/bmp-only owners, some Qt apps offer `application/x-qt-image` +
   `image/png`). Convert via `gdk_pixbuf` to PNG when `image/png` is absent. Pasting a huge image is
   bounded by `MaxFileSize`, and the server may refuse images over `MaxImageResolution`.
6. **`text/uri-list` with non-file URIs** (links). The paste rule checks `getData === ''`, and WebKit
   only hides file paths, so links still paste as text (verified). In Go, accept only `file://`.
   Ignore `x-special/gnome-copied-files` "cut"; treat it as copy.
7. **Files changing between staging and upload.** Re-stat before upload. Directories dropped or copied
   are not supported by `POST /files`, so refuse them with a message.
8. **Pending posts are not persisted.** A restart loses a pending post with attachments, the same as
   today for text. Spools must be swept at startup, asynchronously.
9. **Channel binding.** Uploads are tied to `channel_id`. Moving a draft with attachments to another
   channel means re-uploading.
10. **The Wails file dialog is `gtk_dialog_run` (modal nested loop), not the portal.** Under Flatpak it
    would need the portal; that is not relevant now.
11. **e2e.** Browser mode covers the page → `/api/attachments` → Go → fake upload flow with synthetic
    `ClipboardEvent`/`DragEvent` + `DataTransfer` + `File` (verified in Chromium) and
    `setInputFiles`. The desktop-only paths (GTK clipboard, native drop, dialog) need Go unit tests with
    injected seams (a `Clipboard`/`FilePicker` interface) and optionally a PyGObject/Xvfb smoke test
    like this spike.
12. **`e2e` webServer uses `build/bin/spk-mm-client`.** Do not rebuild it from a feature worktree
    without care (the user's build lives there).
