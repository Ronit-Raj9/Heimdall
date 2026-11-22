'use strict';
// Heimdall recording launcher for Playwright MCP (lane Z03).
//
// It starts the upstream server (/app/cli.js) in this process and adds three
// things the server does not do on its own:
//
//   * Recordings that are complete. Playwright writes a context's video and
//     HAR only when the context closes, and the upstream exit handler kills
//     the browser first, which leaves a truncated video and no HAR. Here
//     SIGTERM closes every browser context before the server exits, so the
//     files under /tmp/rec are whole. Each page's video is saved as
//     /tmp/rec/video/page-<n>.webm with a "<file>.start" sidecar with the epoch milliseconds of its first frame,
//     and console messages and page errors go to /tmp/rec/console.jsonl.
//   * A live screen: `node record.js frame` prints a JPEG of the page the
//     agent used last, for the run page's live view.
//   * Human takeover in a debug run: `node record.js input` reads one
//     action ({"action": "click", "x": 10, "y": 20}, type, key, scroll) on
//     stdin and performs it on that page.
//
// frame and input talk to the server process over 127.0.0.1:8932, which is
// not published and not reachable from the agent (the agent reaches this
// container only through the tool proxy, on the MCP port).
//
// Usage in task.yaml: the image's entrypoint runs this file; the stop
// command is `kill -TERM 1` (tini forwards it here).

const fs = require('fs');
const http = require('http');
const path = require('path');

const REC = process.env.HEIMDALL_REC_DIR || '/tmp/rec';
const CONTROL_PORT = Number(process.env.HEIMDALL_CONTROL_PORT || 8932);
const mode = process.argv[2];

if (mode === 'frame' || mode === 'input') {
  client(mode);
} else {
  server();
}

// ---------------------------------------------------------------- client --

function client(kind) {
  const send = (body) => {
    const req = http.request({ host: '127.0.0.1', port: CONTROL_PORT, path: '/' + kind, method: body ? 'POST' : 'GET', timeout: 15000 }, (res) => {
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => {
        const data = Buffer.concat(chunks);
        if (res.statusCode !== 200) {
          process.stderr.write(data.toString() + '\n');
          process.exit(1);
        }
        if (kind === 'frame') process.stdout.write(data);
        process.exit(0);
      });
    });
    req.on('timeout', () => req.destroy(new Error('timed out')));
    req.on('error', (e) => {
      process.stderr.write(String(e) + '\n');
      process.exit(1);
    });
    req.end(body);
  };
  if (kind === 'frame') return send(null);
  const chunks = [];
  process.stdin.on('data', (c) => chunks.push(c));
  process.stdin.on('end', () => send(Buffer.concat(chunks)));
}

// ---------------------------------------------------------------- server --

function server() {
  fs.mkdirSync(path.join(REC, 'video'), { recursive: true });
  fs.mkdirSync(path.join(REC, 'raw'), { recursive: true });
  fs.mkdirSync(path.join(REC, 'har'), { recursive: true });
  const consoleLog = fs.createWriteStream(path.join(REC, 'console.jsonl'), { flags: 'a' });
  const pw = require('/app/node_modules/playwright-core');
  const contexts = new Set();
  let lastPage = null;
  let harSeq = 0;
  const videos = []; // one saveAs promise per page's video (/tmp/rec/video)

  const watchPage = (page) => {
    const t0 = Date.now();
    lastPage = page;
    const video = page.video && page.video();
    if (video) {
      // The server reaches its browser over Playwright's protocol, so the
      // video's own path is not available here. saveAs, asked at once,
      // copies it to a name we choose as soon as the page closes.
      const out = path.join(REC, 'video', `page-${videos.length + 1}.webm`);
      fs.writeFileSync(out + '.start', String(t0));
      videos.push(video.saveAs(out).catch((e) => process.stderr.write('heimdall: video not saved: ' + e + '\n')));
    }
    page.on('console', (m) => {
      consoleLog.write(JSON.stringify({ ts: new Date().toISOString(), type: m.type(), text: m.text().slice(0, 2000), url: page.url() }) + '\n');
    });
    page.on('pageerror', (e) => {
      consoleLog.write(JSON.stringify({ ts: new Date().toISOString(), type: 'pageerror', text: String(e).slice(0, 2000), url: page.url() }) + '\n');
    });
    page.on('framenavigated', () => { lastPage = page; });
    page.on('close', () => { if (lastPage === page) lastPage = null; });
  };

  const track = (browser) => {
    if (!browser || browser.__heimdallTracked) return browser;
    browser.__heimdallTracked = true;
    const newContext = browser.newContext;
    browser.newContext = async function (options, ...rest) {
      const opts = { ...(options || {}) };
      if (opts.recordHar && opts.recordHar.path) {
        // One HAR per context, so two clients never write one file.
        harSeq += 1;
        opts.recordHar = { ...opts.recordHar, path: path.join(REC, 'har', `context-${harSeq}.har`) };
      }
      const c = await newContext.call(this, opts, ...rest);
      contexts.add(c);
      c.once('close', () => contexts.delete(c));
      c.on('page', watchPage);
      c.pages().forEach(watchPage);
      return c;
    };
    return browser;
  };

  const proto = Object.getPrototypeOf(pw.chromium);
  for (const m of ['launch', 'connect', 'connectOverCDP']) {
    const orig = proto[m];
    if (typeof orig !== 'function') continue;
    proto[m] = async function (...args) {
      return track(await orig.apply(this, args));
    };
  }

  // SIGTERM and SIGINT: close the contexts (which writes the videos and the
  // HARs), then let the upstream handlers exit. A context that does not
  // close in 20 s is left; the exit goes on.
  let closing = null;
  const saveVideos = () => Promise.allSettled(videos);
  const closeAll = () => {
    if (!closing) closing = Promise.allSettled([...contexts].map((c) => c.close())).then(saveVideos);
    return closing;
  };
  const realOn = process.on.bind(process);
  const deferred = { SIGTERM: [], SIGINT: [] };
  process.on = (ev, fn) => {
    if (ev in deferred) {
      deferred[ev].push(fn);
      return process;
    }
    return realOn(ev, fn);
  };
  for (const sig of Object.keys(deferred)) {
    realOn(sig, () => {
      const go = () => deferred[sig].forEach((fn) => fn(sig));
      const t = setTimeout(go, 20000);
      closeAll().then(() => {
        clearTimeout(t);
        consoleLog.end(go);
      });
    });
  }

  // The control port: frame and input for the live view.
  const keyName = (combo) =>
    String(combo)
      .split('+')
      .map((k) => ({ ctrl: 'Control', control: 'Control', alt: 'Alt', shift: 'Shift', meta: 'Meta', super: 'Meta', cmd: 'Meta', enter: 'Enter', return: 'Enter', esc: 'Escape', escape: 'Escape', tab: 'Tab', space: 'Space', backspace: 'Backspace', delete: 'Delete' })[k.toLowerCase()] || k)
      .join('+');
  const perform = async (page, a) => {
    let x = Number(a.x) || 0;
    let y = Number(a.y) || 0;
    const vp = page.viewportSize();
    if (vp && a.frame_w > 0 && a.frame_h > 0) {
      // x and y were read from a live frame of that size.
      x = (x * vp.width) / a.frame_w;
      y = (y * vp.height) / a.frame_h;
    }
    switch (a.action) {
      case 'click':
        return page.mouse.click(x, y);
      case 'type':
        return page.keyboard.type(String(a.text || '').slice(0, 2000), { delay: 15 });
      case 'key':
        return page.keyboard.press(keyName(a.combo));
      case 'scroll': {
        const d = { up: [0, -400], down: [0, 400], left: [-400, 0], right: [400, 0] }[a.direction] || [0, 400];
        return page.mouse.wheel(d[0], d[1]);
      }
      default:
        throw new Error('unknown action ' + a.action);
    }
  };
  const control = http.createServer((req, res) => {
    const fail = (code, msg) => {
      res.writeHead(code, { 'content-type': 'text/plain' });
      res.end(msg);
    };
    const page = lastPage && !lastPage.isClosed() ? lastPage : null;
    if (!page) return fail(404, 'no page is open yet');
    if (req.method === 'GET' && req.url === '/frame') {
      page
        .screenshot({ type: 'jpeg', quality: 55, timeout: 4000, animations: 'allow', caret: 'initial' })
        .then((buf) => {
          res.writeHead(200, { 'content-type': 'image/jpeg' });
          res.end(buf);
        })
        .catch((e) => fail(500, String(e)));
      return;
    }
    if (req.method === 'POST' && req.url === '/input') {
      const chunks = [];
      req.on('data', (c) => chunks.push(c));
      req.on('end', () => {
        let a;
        try {
          a = JSON.parse(Buffer.concat(chunks).toString());
        } catch (e) {
          return fail(400, 'input is not JSON');
        }
        perform(page, a).then(() => fail(200, 'ok')).catch((e) => fail(500, String(e)));
      });
      return;
    }
    fail(404, 'not found');
  });
  control.listen(CONTROL_PORT, '127.0.0.1');
  control.unref();

  process.argv.splice(1, 1, '/app/cli.js');
  require('/app/cli.js');
}
