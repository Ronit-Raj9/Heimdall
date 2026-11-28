import "@/components/connect/ufl6I18n";

import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldRow } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/select";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import type { VerifyOptions } from "@/lib/store/apis/ufl6ConnectApi";
import { useTranslation } from "react-i18next";

/** The "verify on your own task" fields of POST /connect/verify: benchmark, task, or the connection rows only. */
export function VerifyOptionsFields({ value, onChange }: { value: VerifyOptions; onChange: (v: VerifyOptions) => void }) {
	const { t } = useTranslation("ufl6");
	const suites = useGetSuitesLightQuery();
	const off = value.connectionOnly;
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="connect-verify-options">
			<FieldRow>
				<Field label={t("verify.on")}>
					<NativeSelect
						value={value.suite}
						disabled={off}
						onChange={(e) => onChange({ ...value, suite: e.target.value, task: "" })}
						className="min-w-48"
						data-testid="connect-verify-suite"
					>
						<option value="">{t("verify.suiteAuto")}</option>
						{(suites.data?.suites ?? []).map((s) => (
							<option key={s.id} value={s.id}>
								{s.id}
							</option>
						))}
					</NativeSelect>
				</Field>
				<Field label={t("verify.task")}>
					<Input
						value={value.task}
						disabled={off || !value.suite}
						placeholder={t("verify.taskAuto")}
						onChange={(e) => onChange({ ...value, task: e.target.value })}
						className="font-mono"
						data-testid="connect-verify-task"
					/>
				</Field>
			</FieldRow>
			<Checkbox
				checked={off}
				onChange={(c) => onChange({ ...value, connectionOnly: c })}
				label={t("verify.connectionOnly")}
				hint={t("verify.connectionOnlyHint")}
				testId="connect-verify-connection-only"
			/>
		</div>
	);
}