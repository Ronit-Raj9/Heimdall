/**
 * A unified-style line diff of two texts (LCS), as the patch text DiffText renders. Agent configs
 * are small YAML files, so the quadratic table is fine. Context lines start with a space.
 */
export function lineDiff(before: string, after: string, labels: { a: string; b: string }): string {
	if (before === after) return "";
	const a = before.replace(/\n$/, "").split("\n");
	const b = after.replace(/\n$/, "").split("\n");
	const n = a.length;
	const m = b.length;
	const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
	for (let i = n - 1; i >= 0; i--) {
		for (let j = m - 1; j >= 0; j--) {
			lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
		}
	}
	const out = [`--- ${labels.a}`, `+++ ${labels.b}`];
	let i = 0;
	let j = 0;
	while (i < n && j < m) {
		if (a[i] === b[j]) {
			out.push(` ${a[i]}`);
			i++;
			j++;
		} else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
			out.push(`-${a[i++]}`);
		} else {
			out.push(`+${b[j++]}`);
		}
	}
	while (i < n) out.push(`-${a[i++]}`);
	while (j < m) out.push(`+${b[j++]}`);
	return `${out.join("\n")}\n`;
}
