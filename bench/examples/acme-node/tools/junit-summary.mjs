#!/usr/bin/env node
// Summarise a JUnit XML report the way the junit verifier result is judged
// (docs/bring-your-own-design.md, "Suites in any language"): pass only with
// at least one test, no failures, errors or skips, and every required test
// run and passed.
//
//   node junit-summary.mjs REPORT.xml [REQUIRED_ID ...]
//
// A required id is "classname::name". Node's junit reporter writes classname
// "test" for every case, so the ids look like "test::my_test_name". Prints one
// JSON line and exits 0 on pass, 1 on fail, 2 when the report is unreadable.
// Testcases directly under <testsuites> count too (Node writes top-level
// tests and files that fail to load there). Dependency-free on purpose.

import { readFileSync } from 'node:fs';

const [file, ...required] = process.argv.slice(2);
if (!file) {
  console.error('usage: junit-summary.mjs REPORT.xml [REQUIRED_ID ...]');
  process.exit(2);
}

let xml;
try {
  xml = readFileSync(file, 'utf8');
} catch (err) {
  console.log(JSON.stringify({ pass: false, reason: 'test_run_broken', detail: `cannot read ${file}: ${err.code}` }));
  process.exit(2);
}
if (!/<testsuites?\b/.test(xml)) {
  console.log(JSON.stringify({ pass: false, reason: 'test_run_broken', detail: 'no <testsuites> or <testsuite> element' }));
  process.exit(2);
}

const cases = [];
// Attribute values may hold ">" (Node escapes only &, < and " in them), so
// quoted values are skipped as a whole.
const CASE_RE = /<testcase\b((?:[^>"]|"[^"]*")*?)(?:\/>|>([\s\S]*?)<\/testcase>)/g;
for (const m of xml.matchAll(CASE_RE)) {
  const attrs = attributes(m[1]);
  const body = m[2] ?? '';
  cases.push({
    id: `${attrs.classname ?? ''}::${attrs.name ?? ''}`,
    failed: /<failure\b/.test(body),
    errored: /<error\b/.test(body),
    skipped: /<skipped\b/.test(body),
  });
}

const failures = cases.filter((c) => c.failed).length;
const errors = cases.filter((c) => c.errored).length;
const skipped = cases.filter((c) => c.skipped).length;
const missing = [];
const failedRequired = [];
for (const id of required) {
  const matches = cases.filter((c) => c.id === id);
  if (matches.some((c) => c.failed || c.errored)) failedRequired.push(id);
  else if (matches.length === 0 || matches.every((c) => c.skipped)) missing.push(id);
}

const pass = cases.length > 0 && failures === 0 && errors === 0 && skipped === 0 && missing.length === 0 && failedRequired.length === 0;
const failedNames = cases.filter((c) => c.failed || c.errored).map((c) => c.id);
console.log(JSON.stringify({ pass, tests: cases.length, failures, errors, skipped, missing_required: missing, failed: failedNames }));
process.exit(pass ? 0 : 1);

function attributes(text) {
  const out = {};
  for (const [, key, value] of text.matchAll(/([\w:-]+)="([^"]*)"/g)) out[key] = unescapeXml(value);
  return out;
}

function unescapeXml(text) {
  return text
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&#(\d+);/g, (_, n) => String.fromCodePoint(Number(n)))
    .replace(/&amp;/g, '&');
}
