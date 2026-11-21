/** GET /operate/status (handlers/operate.go). */
export interface OperateStatus {
	hosted: boolean;
	/** Operate job kinds this server runs through POST /jobs. */
	kinds: string[];
	/** "folder", "s3" or "" when no evidence mirror is configured. */
	mirror: "" | "folder" | "s3";
	mirror_location?: string;
	gpu_mode?: string;
	driver?: string;
	hint?: string;
}

/** One evidence backup zip (GET /operate/backups). */
export interface OperateBackup {
	name: string;
	size: number;
	mod_time: string;
}

export interface OperateBackupList {
	backups: OperateBackup[];
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		operate: typeof import("@/locales/en/operate.json");
	}
}
