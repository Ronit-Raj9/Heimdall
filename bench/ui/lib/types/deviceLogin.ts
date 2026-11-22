// Device login (RFC 8628, lane Z02): bench/auth/device.go.
export type DeviceScope = "read" | "evaluate" | "admin";

export interface DeviceInfo {
	user_code: string;
	client_name: string;
	client_ip: string;
	scope: DeviceScope;
	created_at: string;
	expires_at: string;
	status: "pending" | "approving" | "approved" | "denied";
}

export interface DeviceApproved {
	user_code: string;
	org_id: string;
	role: string;
	scopes: DeviceScope[];
	token_id: string;
}
