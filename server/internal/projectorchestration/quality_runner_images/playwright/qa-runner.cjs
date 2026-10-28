'use strict';

const fs = require('node:fs/promises');
const path = require('node:path');
const { spawn } = require('node:child_process');

const MAX_STEPS = 30;
const MAX_SCREENSHOTS = 6;
const MAX_SCREENSHOT_BYTES = 1024 * 1024;
const MAX_SCREENSHOT_TOTAL_BYTES = 3 * 1024 * 1024;
const MAX_LINE = 4096;

function bounded(value, limit = MAX_LINE) {
  return String(value || '').replace(/[\r\n\0]+/g, ' ').slice(0, limit);
}

function safeURL(value) {
  try {
    const url = new URL(value);
    return `${url.origin}${url.pathname}`;
  } catch {
    return '<invalid-url>';
  }
}

function isLoopback(host) {
  return host === 'localhost' || host === '127.0.0.1' || host === '[::1]';
}

function parseTarget(scenario) {
  const target = new URL(scenario.target_url);
  if (target.protocol !== 'http:' || target.username || target.password || !isLoopback(target.hostname)) {
    throw new Error('browser QA target must be an HTTP loopback preview URL');
  }
  if (!Array.isArray(scenario.preview_command) || scenario.preview_command.length < 1 || scenario.preview_command.length > 16) {
    throw new Error('browser QA preview command is missing or invalid');
  }
  if (!['corepack', 'pnpm', 'npm', 'node'].includes(scenario.preview_command[0])) {
    throw new Error('browser QA preview command is not allowed');
  }
  if (!Array.isArray(scenario.steps) || scenario.steps.length < 1 || scenario.steps.length > MAX_STEPS) {
    throw new Error('browser QA requires between 1 and 30 journey steps');
  }
  if (!scenario.viewport || !Number.isInteger(scenario.viewport.width) || !Number.isInteger(scenario.viewport.height) ||
      scenario.viewport.width < 320 || scenario.viewport.width > 3840 ||
      scenario.viewport.height < 320 || scenario.viewport.height > 2160) {
    throw new Error('browser QA viewport is invalid');
  }
  return target;
}

function recordUnique(list, value) {
  const entry = bounded(value);
  if (entry && !list.includes(entry) && list.length < 100) list.push(entry);
}

function parseStep(step) {
  const value = String(step || '').trim();
  for (const action of ['click', 'assert-visible', 'assert-hidden']) {
    const prefix = `${action} `;
    if (value.startsWith(prefix) && value.slice(prefix.length).trim()) {
      return { action, selector: value.slice(prefix.length).trim() };
    }
  }
  for (const action of ['fill', 'assert-text']) {
    const prefix = `${action} `;
    if (value.startsWith(prefix)) {
      const [selector, content, found] = splitAssignment(value.slice(prefix.length));
      if (found && selector.trim() && content.trim()) return { action, selector: selector.trim(), content: content.trim() };
      throw new Error(`${action} step must use '<selector> = <value>'`);
    }
  }
  if (value.startsWith('assert-url ')) {
    const expected = value.slice('assert-url '.length).trim();
    if (!expected.startsWith('/') || expected.startsWith('//')) throw new Error('assert-url requires a local absolute path');
    return { action: 'assert-url', expected };
  }
  throw new Error('unsupported browser QA step');
}

function splitAssignment(value) {
  const index = value.indexOf('=');
  return index < 0 ? ['', '', false] : [value.slice(0, index), value.slice(index + 1), true];
}

function previewEnvironment() {
  return {
    HOME: '/tmp/home',
    TMPDIR: '/tmp',
    PATH: '/usr/local/bin:/usr/bin:/bin',
    CI: '1',
    NO_COLOR: '1',
    COREPACK_HOME: '/opt/corepack',
    COREPACK_ENABLE_NETWORK: '0',
    COREPACK_DEFAULT_TO_LATEST: '0',
    npm_config_offline: 'true',
    npm_config_userconfig: '/dev/null',
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: '1',
  };
}

async function waitForPreview(child, target) {
  const deadline = Date.now() + 30000;
  let lastError = 'preview server did not become ready';
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`preview command exited with status ${child.exitCode}`);
    try {
      const response = await fetch(target.href, { redirect: 'manual', signal: AbortSignal.timeout(1000) });
      if (response.status >= 200 && response.status < 400) return;
      lastError = `preview returned HTTP ${response.status}`;
    } catch (error) {
      lastError = bounded(error.message, 512);
    }
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  throw new Error(`preview readiness timed out: ${lastError}`);
}

async function main() {
  const [scenarioPath, outputDir] = process.argv.slice(2);
  const report = {
    version: 1,
    status: 'failed',
    console_errors: [],
    network_failures: [],
    screenshots: [],
    execution_trace: [],
    error: '',
  };
  let preview;
  let browser;
  let totalScreenshotBytes = 0;
  let screenshotSequence = 0;

  const writeReport = async () => {
    await fs.mkdir(outputDir, { recursive: true });
    await fs.writeFile(path.join(outputDir, 'result.json'), JSON.stringify(report), { mode: 0o644 });
  };
  const capture = async page => {
    if (!page || report.screenshots.length >= MAX_SCREENSHOTS) return;
    try {
      const image = await page.screenshot({ type: 'jpeg', quality: 75, fullPage: false, timeout: 5000 });
      totalScreenshotBytes += image.length;
      if (image.length > MAX_SCREENSHOT_BYTES || totalScreenshotBytes > MAX_SCREENSHOT_TOTAL_BYTES) {
        throw new Error('screenshot evidence exceeded its size limit');
      }
      const name = `qa-${String(++screenshotSequence).padStart(4, '0')}.jpg`;
      await fs.writeFile(path.join(outputDir, name), image, { mode: 0o644, flag: 'wx' });
      report.screenshots.push(name);
    } catch (error) {
      report.error = report.error || `screenshot capture failed: ${bounded(error.message, 512)}`;
      report.execution_trace.push('screenshot capture failed');
    }
  };

  try {
    if (!scenarioPath || !outputDir) throw new Error('browser QA input/output paths are missing');
    const scenario = JSON.parse(await fs.readFile(scenarioPath, 'utf8'));
    const target = parseTarget(scenario);
    const [program, ...args] = scenario.preview_command;
    preview = spawn(program, args, {
      cwd: '/tmp/task',
      env: previewEnvironment(),
      stdio: 'ignore',
      shell: false,
    });
    preview.once('error', error => { report.error = report.error || `preview command failed: ${bounded(error.message, 512)}`; });
    await waitForPreview(preview, target);

    let playwright;
    try {
      playwright = require('/usr/lib/node_modules/playwright');
    } catch (error) {
      throw new Error(`Playwright is unavailable in the pinned runner image: ${bounded(error.message, 512)}`);
    }
    browser = await playwright.chromium.launch({
      headless: true,
      args: ['--no-sandbox', '--disable-dev-shm-usage'],
    });
    const context = await browser.newContext({ viewport: scenario.viewport });
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    page.on('console', message => {
      if (message.type() === 'error') recordUnique(report.console_errors, `${bounded(message.text())} (${safeURL(page.url())})`);
    });
    page.on('pageerror', error => recordUnique(report.console_errors, `${bounded(error.message)} (${safeURL(page.url())})`));
    page.on('requestfailed', request => {
      const reason = request.failure()?.errorText || 'request failed';
      recordUnique(report.network_failures, `${safeURL(request.url())}: ${bounded(reason, 512)}`);
    });
    page.on('response', response => {
      if (response.status() >= 400) recordUnique(report.network_failures, `${safeURL(response.url())}: HTTP ${response.status()}`);
    });
    await page.route('**/*', route => {
      const requested = new URL(route.request().url());
      if (requested.protocol !== 'http:' && requested.protocol !== 'https:' && requested.protocol !== 'ws:' && requested.protocol !== 'wss:') {
        return route.continue();
      }
      const expectedOrigin = requested.protocol === 'ws:' ? target.origin.replace(/^http:/, 'ws:') :
        requested.protocol === 'wss:' ? target.origin.replace(/^https:/, 'wss:') : target.origin;
      if (requested.origin !== expectedOrigin) {
        recordUnique(report.network_failures, `blocked cross-origin request: ${safeURL(requested.href)}`);
        return route.abort('blockedbyclient');
      }
      return route.continue();
    });

    await page.goto(target.href, { waitUntil: 'domcontentloaded', timeout: 15000 });
    report.execution_trace.push(`opened ${safeURL(page.url())}`);
    if (scenario.capture_screenshots !== false) await capture(page);

    for (let index = 0; index < scenario.steps.length; index += 1) {
      const step = parseStep(scenario.steps[index]);
      try {
        if (step.action === 'click') {
          await page.locator(step.selector).first().click();
        } else if (step.action === 'fill') {
          await page.locator(step.selector).first().fill(step.content);
        } else if (step.action === 'assert-visible') {
          if (!await page.locator(step.selector).first().isVisible()) throw new Error('expected element to be visible');
        } else if (step.action === 'assert-hidden') {
          const locator = page.locator(step.selector).first();
          if (await locator.count() > 0 && await locator.isVisible()) throw new Error('expected element to be hidden');
        } else if (step.action === 'assert-text') {
          const actual = (await page.locator(step.selector).first().textContent()) || '';
          if (!actual.includes(step.content)) throw new Error('expected text was not present');
        } else if (step.action === 'assert-url') {
          await page.waitForURL(value => {
            const current = new URL(value.href);
            return `${current.pathname}${current.search}${current.hash}` === step.expected;
          }, { timeout: 10000 });
        }
        report.execution_trace.push(`step ${index + 1}: ${step.action} passed`);
      } catch (error) {
        report.execution_trace.push(`step ${index + 1}: ${step.action} failed: ${bounded(error.message, 512)}`);
        report.error = report.error || `browser QA step ${index + 1} failed`;
        break;
      }
      if (scenario.capture_screenshots !== false && index < 4) await capture(page);
    }

    await page.waitForTimeout(50);
    if (report.console_errors.length || report.network_failures.length || report.error || report.screenshots.length === 0) {
      await capture(page);
    }
    if (report.console_errors.length || report.network_failures.length) {
      report.error = report.error || 'browser QA detected console or network failures';
    }
    report.status = report.console_errors.length === 0 && report.network_failures.length === 0 &&
      report.error === '' && report.execution_trace.length === scenario.steps.length + 1 ? 'passed' : 'failed';
    await context.close();
  } catch (error) {
    report.error = report.error || bounded(error.message, 4096);
    report.execution_trace.push(`browser QA failed: ${bounded(error.message, 512)}`);
  } finally {
    if (browser) await browser.close().catch(() => {});
    if (preview && preview.exitCode === null) {
      preview.kill('SIGTERM');
      await new Promise(resolve => {
        const timeout = setTimeout(() => { preview.kill('SIGKILL'); resolve(); }, 2000);
        preview.once('exit', () => { clearTimeout(timeout); resolve(); });
      });
    }
    try {
      await writeReport();
    } catch (error) {
      process.stderr.write(`could not persist browser QA report: ${bounded(error.message, 512)}\n`);
      process.exitCode = 125;
      return;
    }
  }

  process.exitCode = report.status === 'passed' ? 0 : 1;
}

main().catch(error => {
  process.stderr.write(`browser QA runner failed: ${bounded(error.message, 1024)}\n`);
  process.exitCode = 125;
});
