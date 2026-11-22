package tenancy

// Device login (RFC 8628, lane Z02, handlers/authdevice.go): `heimdall login`.
func init() {
	PlatformPolicy[key("POST", api+"/auth/device/code")] = public()  // starts a login; rate limited per IP; grants nothing by itself
	PlatformPolicy[key("POST", api+"/auth/device/token")] = public() // the device code is the credential; the token is handed over once
	PlatformPolicy[key("GET", api+"/auth/device")] = userOnly()      // approval page lookup, rate limited per user
	PlatformPolicy[key("POST", api+"/auth/device/approve")] = userOnly()
	PlatformPolicy[key("POST", api+"/auth/device/deny")] = userOnly()
}
