/**
 * The label of evaluation arm i: "A", "B", "C" and so on (fix wave 3, N211). The server takes an arm
 * label only when it is a capital letter followed by letters or digits (`evalArmIDRe` in
 * `handlers/evaluations.go`), and labels become experiment arm ids, so every form labels arms here.
 */
export function evalArmLabel(i: number): string {
	return String.fromCharCode(65 + i);
}