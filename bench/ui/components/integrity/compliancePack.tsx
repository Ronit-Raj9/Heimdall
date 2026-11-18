import { Button } from "@/components/ui/button";
import { compliancePackUrl } from "@/lib/store/apis/integrityApi";
import { FileJson, FileText } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * Download links for an experiment's compliance evidence pack (Eval integrity area, S2). The
 * server builds the pack on request, verifying every seal and the chain, so the files always
 * describe the evidence as it is now.
 */
export function CompliancePackLinks({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col gap-3" data-testid="compliance-pack">
			<div className="flex flex-wrap gap-2">
				<Button variant="outline" size="sm" asChild>
					<a href={compliancePackUrl(expId, "md")} download={`compliance-${expId}.md`} data-testid="compliance-pack-md">
						<FileText className="size-4" aria-hidden="true" />
						{t("integrity.compliance.markdown")}
					</a>
				</Button>
				<Button variant="outline" size="sm" asChild>
					<a href={compliancePackUrl(expId, "json")} download={`compliance-${expId}.json`} data-testid="compliance-pack-json">
						<FileJson className="size-4" aria-hidden="true" />
						{t("integrity.compliance.json")}
					</a>
				</Button>
			</div>
			<p className="text-muted-foreground text-xs">{t("integrity.compliance.disclaimer")}</p>
		</div>
	);
}