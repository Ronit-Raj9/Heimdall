// Hidden tests for add.js, run by node --test with the junit reporter.
const test = require('node:test');
const assert = require('node:assert');
const { add } = require('/workspace/add.js');

test('adds', () => {
	assert.strictEqual(add(2, 3), 5);
});

test.describe('signs', () => {
	test.it('adds negatives', () => {
		assert.strictEqual(add(-2, -3), -5);
	});
});
