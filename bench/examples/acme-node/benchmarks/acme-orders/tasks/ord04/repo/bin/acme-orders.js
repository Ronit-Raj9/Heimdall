#!/usr/bin/env node
// Entry point of the acme-orders command; see src/cli.js.
import { main } from '../src/cli.js';

process.exitCode = main(process.argv.slice(2));
