import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
  platformStatusTone,
  useConnectWorkerPlatformMutation,
  useDisconnectWorkerPlatformMutation,
  useListWorkerPlatformsQuery,
  useSetDefaultWorkerMutation,
  useTestWorkerPlatformMutation,
  type WorkerPlatform,
} from "@/lib/store/apis/workerPlatformsApi";
import { cn } from "@/lib/utils";
import { CheckCircle2, Cloud, ExternalLink, PlugZap } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { SettingsSection, When } from "./settingsBits";

declare module "@/lib/i18n/resources" {
  interface LaneResources {
    workerPlatforms: typeof import("@/locales/en/workerPlatforms.json");
  }
}

/** True when the server answered that platforms are off here (local mode or no master key). */
export function platformsOff(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "status" in error &&
    (error as { status: unknown }).status === 503
  );
}

export interface PlatformCardProps {
  platform: WorkerPlatform;
  busy?: boolean;
  error?: string;
  notice?: string;
  onConnect: (token: string, endpoint: string) => void;
  onTest: () => void;
  onDefault: () => void;
  onDisconnect: () => void;
}

/** One platform: its state, the token form, and its actions. Pure, so it renders on the server in tests. */
export function PlatformCard({
  platform: p,
  busy,
  error,
  notice,
  onConnect,
  onTest,
  onDefault,
  onDisconnect,
}: PlatformCardProps) {
  const { t } = useTranslation("workerPlatforms");
  const formId = useId();
  const [editing, setEditing] = useState(!p.connected);
  const [token, setToken] = useState("");
  const [endpoint, setEndpoint] = useState(p.endpoint ?? "");
  const showForm = editing || !p.connected;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!token.trim() || (p.endpoint_required && !endpoint.trim())) return;
    onConnect(token.trim(), endpoint.trim());
    setToken("");
  };
  const status = p.status ?? "untested";
  return (
    <article
      className={cn(
        "bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5 transition-shadow hover:shadow-md",
        p.default && "border-primary/40",
      )}
      data-testid={`platform-${p.name}`}
      data-connected={p.connected}
    >
      <header className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
            <Cloud className="size-4" aria-hidden="true" />
          </span>
          <div className="min-w-0">
            <p className="text-foreground truncate text-sm font-semibold">
              {p.title}
            </p>
            <p className="text-muted-foreground truncate text-xs">
              {p.connected
                ? p.token_last4
                  ? t("tokenEnding", { last4: p.token_last4 })
                  : t("connected")
                : t("notConnected")}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {p.default && (
            <Badge variant="default" data-testid={`platform-${p.name}-default`}>
              {t("isDefault")}
            </Badge>
          )}
          {p.connected && (
            <Badge
              variant={platformStatusTone(status)}
              data-testid={`platform-${p.name}-status`}
            >
              {t(`status.${status}` as "status.ok", { defaultValue: status })}
            </Badge>
          )}
        </div>
      </header>
      {p.connected && p.last_tested_at && (
        <p className="text-muted-foreground text-xs">
          {t("lastTested")} <When value={p.last_tested_at} />
          {p.endpoint ? (
            <span className="font-mono"> · {p.endpoint}</span>
          ) : null}
        </p>
      )}
      {showForm ? (
        <form
          className="flex flex-col gap-3"
          onSubmit={submit}
          noValidate
          data-testid={`platform-${p.name}-form`}
        >
          <div className="flex flex-col gap-1.5">
            <label
              htmlFor={`${formId}-token`}
              className="text-[13px] font-medium"
            >
              {t("tokenLabel")}
            </label>
            <Input
              id={`${formId}-token`}
              type="password"
              autoComplete="off"
              spellCheck={false}
              placeholder={t("tokenPlaceholder")}
              value={token}
              onChange={(event) => setToken(event.target.value)}
              className="font-mono"
              data-testid={`platform-${p.name}-token`}
            />
            <span className="text-muted-foreground text-xs">
              {p.token_hint}
            </span>
          </div>
          {p.endpoint_label && (
            <div className="flex flex-col gap-1.5">
              <label
                htmlFor={`${formId}-endpoint`}
                className="text-[13px] font-medium"
              >
                {p.endpoint_label}
              </label>
              <Input
                id={`${formId}-endpoint`}
                autoComplete="off"
                value={endpoint}
                onChange={(event) => setEndpoint(event.target.value)}
                className="font-mono"
                data-testid={`platform-${p.name}-endpoint`}
              />
            </div>
          )}
          <div className="flex flex-wrap items-center justify-between gap-2">
            {p.docs_url ? (
              <a
                href={p.docs_url}
                target="_blank"
                rel="noreferrer"
                className="text-primary inline-flex items-center gap-1 text-xs font-medium underline-offset-4 hover:underline"
              >
                {t("getToken")}
                <ExternalLink className="size-3" aria-hidden="true" />
              </a>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
              {p.connected && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setEditing(false)}
                >
                  {t("cancel")}
                </Button>
              )}
              <Button
                type="submit"
                size="sm"
                className="gap-1.5"
                isLoading={busy}
                disabled={
                  !token.trim() || (p.endpoint_required && !endpoint.trim())
                }
                dataTestId={`platform-${p.name}-connect`}
              >
                <PlugZap className="size-4" aria-hidden="true" />
                {t("connect")}
              </Button>
            </div>
          </div>
        </form>
      ) : (
        <div className="flex flex-wrap items-center justify-end gap-2 border-t pt-3">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setEditing(true)}
            dataTestId={`platform-${p.name}-replace`}
          >
            {t("replace")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={onTest}
            isLoading={busy}
            dataTestId={`platform-${p.name}-test`}
          >
            {t("test")}
          </Button>
          {!p.default && (
            <Button
              variant="outline"
              size="sm"
              onClick={onDefault}
              disabled={busy}
              dataTestId={`platform-${p.name}-make-default`}
            >
              {t("makeDefault")}
            </Button>
          )}
          <ConfirmButton
            label={t("disconnect")}
            question={t("confirmDisconnect", { platform: p.title })}
            confirmLabel={t("disconnect")}
            cancelLabel={t("cancel")}
            onConfirm={onDisconnect}
            disabled={busy}
            testId={`platform-${p.name}-disconnect`}
          />
        </div>
      )}
      {notice && (
        <p
          className="text-chart-success-ink flex items-center gap-1.5 text-xs"
          role="status"
          data-testid={`platform-${p.name}-notice`}
        >
          <CheckCircle2 className="size-3.5" aria-hidden="true" />
          {notice}
        </p>
      )}
      {error && (
        <p
          role="alert"
          className="text-chart-error-ink text-xs break-words"
          data-testid={`platform-${p.name}-error`}
        >
          {t("error", { platform: p.title })}: {error}
        </p>
      )}
    </article>
  );
}

/** Settings, Workers: connect Modal, Daytona or E2B with the org's own token and choose the default worker. */
export function WorkerPlatformsSection() {
  const { t } = useTranslation("workerPlatforms");
  const list = useListWorkerPlatformsQuery();
  const [connect, connectState] = useConnectWorkerPlatformMutation();
  const [test, testState] = useTestWorkerPlatformMutation();
  const [setDefault, defaultState] = useSetDefaultWorkerMutation();
  const [disconnect, disconnectState] = useDisconnectWorkerPlatformMutation();
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [notices, setNotices] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState("");

  const run = async (
    name: string,
    action: () => Promise<unknown>,
    notice?: string,
  ) => {
    setBusy(name);
    setErrors((e) => ({ ...e, [name]: "" }));
    setNotices((n) => ({ ...n, [name]: "" }));
    try {
      await action();
      if (notice) setNotices((n) => ({ ...n, [name]: notice }));
    } catch (error) {
      setErrors((e) => ({ ...e, [name]: getBenchErrorMessage(error) }));
    } finally {
      setBusy("");
    }
  };

  if (list.isError && platformsOff(list.error)) {
    return (
      <SettingsSection
        title={t("title")}
        description={t("off")}
        testId="settings-worker-platforms"
      >
        <span />
      </SettingsSection>
    );
  }
  const data = list.data;
  const current = data?.platforms.find((p) => p.name === data.default);
  const anyBusy =
    connectState.isLoading ||
    testState.isLoading ||
    defaultState.isLoading ||
    disconnectState.isLoading;
  return (
    <SettingsSection
      title={t("title")}
      description={t("description")}
      testId="settings-worker-platforms"
      actions={
        data?.default ? (
          <Button
            variant="outline"
            size="sm"
            disabled={anyBusy}
            onClick={() => void run("shared", () => setDefault("").unwrap())}
            dataTestId="platform-use-shared"
          >
            {t("useShared")}
          </Button>
        ) : undefined
      }
    >
      {list.isLoading ? (
        <BlockSkeleton rows={2} testId="platforms-loading" />
      ) : list.isError || !data ? (
        <LoadError
          title={t("loadError")}
          error={list.error}
          onRetry={() => void list.refetch()}
          testId="platforms-error"
        />
      ) : (
        <>
          <p
            className="text-muted-foreground text-xs"
            data-testid="platforms-default-note"
          >
            <span className="text-foreground font-medium">
              {t("defaultLabel")}:
            </span>{" "}
            {current ? current.title : t("shared")}.{" "}
            {t("defaultNote", {
              worker: current ? current.title : t("shared"),
            })}
          </p>
          {errors.shared && (
            <p role="alert" className="text-chart-error-ink text-xs">
              {errors.shared}
            </p>
          )}
          <div
            className="grid gap-3 md:grid-cols-3"
            data-testid="platforms-grid"
          >
            {data.platforms.map((p) => (
              <PlatformCard
                key={`${p.name}-${p.connected}-${p.token_last4 ?? ""}`}
                platform={p}
                busy={busy === p.name}
                error={errors[p.name] || undefined}
                notice={notices[p.name] || undefined}
                onConnect={(token, endpoint) =>
                  void run(p.name, () =>
                    connect({ platform: p.name, token, endpoint }).unwrap(),
                  )
                }
                onTest={() =>
                  void run(p.name, () => test(p.name).unwrap(), t("testOk"))
                }
                onDefault={() =>
                  void run(p.name, () => setDefault(p.name).unwrap())
                }
                onDisconnect={() =>
                  void run(p.name, () => disconnect(p.name).unwrap())
                }
              />
            ))}
          </div>
        </>
      )}
    </SettingsSection>
  );
}
