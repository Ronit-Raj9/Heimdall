import type { OverflowMenuItem } from "@/components/agents/overflowMenu";
import { Button } from "@/components/ui/button";
import { DropdownMenu } from "@/components/ui/dropdownMenu";
import { BENCH_API_BASE } from "@/lib/store/apis/fixtures";
import { ChevronDown, Download } from "lucide-react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { runTraceUrl } from "./traceView.utils";

const ITEMS = [
  { format: "json", ext: "trace.json" },
  { format: "otlp", ext: "otlp.json" },
  { format: "jsonl", ext: "steps.jsonl" },
] as const;

/** Starts a file download of url as name (a menu item is a button, not a link). */
function download(url: string, name: string) {
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/** The three trace downloads as menu items: JSON, OTLP JSON (only once sealed), one step per line. */
export function runExportItems(runId: string, sealed: boolean, t: TFunction<"runTrace">): OverflowMenuItem[] {
  return ITEMS.map(({ format, ext }) => {
    const disabled = format === "otlp" && !sealed;
    return {
      label: t(`export.${format}`),
      icon: Download,
      disabled,
      disabledReason: disabled ? t(`export.${format}Note`) : undefined,
      onSelect: () => download(runTraceUrl(BENCH_API_BASE, runId, format), `${runId}.${ext}`),
      testId: `run-export-${format}`,
    };
  });
}

/** The run trace Export menu, on the shared dropdown: JSON, OTLP JSON, or one step per line. */
export function RunExportMenu({
  runId,
  sealed,
}: {
  runId: string;
  sealed: boolean;
}) {
  const { t } = useTranslation("runTrace");
  return (
    <DropdownMenu
      label={t("export.button")}
      items={runExportItems(runId, sealed, t)}
      testId="run-export"
      trigger={(props) => (
        <Button {...props} variant="outline" size="sm" dataTestId="run-export-button">
          <Download aria-hidden="true" />
          {t("export.button")}
          <ChevronDown aria-hidden="true" />
        </Button>
      )}
    />
  );
}
