// The commands of the bring-your-own contract (docs/bring-your-own-design.md), one list per tab.
// Commands are code and are not translated; each step's explanation is an i18n key under
// suites.add.<tab>. Angle-bracket parts are for the reader to fill in.

export type AddTab = "task" | "suite" | "agent";

export const ADD_TABS: readonly AddTab[] = ["task", "suite", "agent"];

export interface AddStep {
	/** Stable id, for test ids. */
	key: string;
	/** The one-line explanation. */
	labelKey:
		| "suites.add.task.init"
		| "suites.add.task.taskInit"
		| "suites.add.task.edit"
		| "suites.add.task.validate"
		| "suites.add.task.admit"
		| "suites.add.suite.init"
		| "suites.add.suite.image"
		| "suites.add.suite.verifier"
		| "suites.add.suite.lock"
		| "suites.add.agent.init"
		| "suites.add.agent.build"
		| "suites.add.agent.check"
		| "suites.add.agent.contract";
	/** What to run or write, copied as is. */
	code: string;
	/** "shell" for a command, "yaml" for a file snippet. */
	lang: "shell" | "yaml";
}

export const ADD_STEPS: Record<AddTab, readonly AddStep[]> = {
	task: [
		{ key: "init", labelKey: "suites.add.task.init", lang: "shell", code: "heimdall init <dir>" },
		{
			key: "taskInit",
			labelKey: "suites.add.task.taskInit",
			lang: "shell",
			code: [
				"heimdall task init -suite <suite> -id <task> \\",
				"    -repo <git repo path> -base <commit> -reference <commit> \\",
				"    -tests <glob,...> -license <spdx|proprietary> -capabilities a,b",
			].join("\n"),
		},
		{ key: "edit", labelKey: "suites.add.task.edit", lang: "shell", code: "$EDITOR benchmarks/<suite>/tasks/<task>/instruction.md" },
		{ key: "validate", labelKey: "suites.add.task.validate", lang: "shell", code: "heimdall task validate <suite>/<task>" },
		{ key: "admit", labelKey: "suites.add.task.admit", lang: "shell", code: "heimdall task admit <suite>/<task>" },
	],
	suite: [
		{ key: "init", labelKey: "suites.add.suite.init", lang: "shell", code: "heimdall suite init <id> -template node-test" },
		{ key: "image", labelKey: "suites.add.suite.image", lang: "shell", code: "$EDITOR benchmarks/<id>/base/Dockerfile" },
		{
			key: "verifier",
			labelKey: "suites.add.suite.verifier",
			lang: "yaml",
			code: [
				"# benchmarks/<id>/suite.yaml",
				"verifier:",
				'  command: ["sh", "/grader/tests/test.sh"]',
				"  result: reward            # junit (default) | reward | exit_code",
				"  result_path: /grader/out/reward.json",
				"  pass_threshold: 1",
			].join("\n"),
		},
		{
			key: "lock",
			labelKey: "suites.add.suite.lock",
			lang: "shell",
			code: "heimdall suite lock <id>\nheimdall suite admit <id>",
		},
	],
	agent: [
		{ key: "init", labelKey: "suites.add.agent.init", lang: "shell", code: "heimdall agent init <id> -template http-service" },
		{ key: "build", labelKey: "suites.add.agent.build", lang: "shell", code: "heimdall agent build agents/<id>.yaml -suite <suite>" },
		{
			key: "check",
			labelKey: "suites.add.agent.check",
			lang: "shell",
			code: "heimdall agent check agents/<id>.yaml -suite <suite> [-task <task>]",
		},
		{
			key: "contract",
			labelKey: "suites.add.agent.contract",
			lang: "yaml",
			code: [
				"# agents/<id>.yaml",
				"env:",
				'  BENCH_BASE_URL: "{{gateway.url}}/openai/v1"',
				'  BENCH_API_KEY: "{{run.key}}"',
				"build:",
				"  base_arg: SUITE_BASE",
			].join("\n"),
		},
	],
};