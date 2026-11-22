package platformimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

// checkUserURL runs the client's URL guard on an address the user typed.
func (c *Client) checkUserURL(raw string) error {
	if c.CheckURL != nil {
		return c.CheckURL(raw)
	}
	return nil
}

// ---- AWS Bedrock AgentCore -------------------------------------------------

var (
	agentCoreARNRe = regexp.MustCompile(`^arn:aws[a-z-]*:bedrock-agentcore:([a-z0-9-]+):([0-9]{12}):runtime/([A-Za-z][A-Za-z0-9_]{0,47}-[A-Za-z0-9]{10})$`)
	agentCoreIDRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,47}-[A-Za-z0-9]{10}$`)
	awsRegionRe    = regexp.MustCompile(`^[a-z]{2}(-gov)?-[a-z]+-[0-9]$`)
	qualifierRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// AgentCoreSessionHeader carries the runtime session id; AgentCore wants at
// least 33 characters, so Heimdall's session id is padded with a prefix.
const AgentCoreSessionHeader = "X-Amzn-Bedrock-AgentCore-Runtime-Session-Id"

func importAgentCore(ctx context.Context, c *Client, j *job) error {
	region, runtimeID, arn := j.input("region"), "", ""
	if m := agentCoreARNRe.FindStringSubmatch(j.id); m != nil {
		arn, runtimeID = j.id, m[3]
		if region != "" && region != m[1] {
			return errorf("the region %s does not match the ARN's region %s", region, m[1])
		}
		region = m[1]
	} else if agentCoreIDRe.MatchString(j.id) {
		runtimeID = j.id
	} else {
		return errorf("%q is not an AgentCore runtime ARN (arn:aws:bedrock-agentcore:<region>:<account>:runtime/<id>) or runtime id", j.id)
	}
	if region == "" {
		return errors.New("AWS Bedrock AgentCore needs the region (or paste the runtime ARN)")
	}
	if !awsRegionRe.MatchString(region) {
		return errorf("%q is not an AWS region", region)
	}
	qualifier := firstOf(j.input("qualifier"), "DEFAULT")
	if !qualifierRe.MatchString(qualifier) {
		return errorf("endpoint qualifier %q: want letters, digits, _ and -", qualifier)
	}
	src := "https://bedrock-agentcore-control." + region + ".amazonaws.com/runtimes/" + url.PathEscape(runtimeID) + "/"
	ak, sk, st := j.secret(SecretAccessKey), j.secret(SecretSecretKey), j.secret(SecretSessionToken)
	doc, err := c.getJSON(ctx, call{method: http.MethodGet, url: src, what: "the AgentCore runtime", secrets: j.secretValues(),
		sign: func(r *http.Request, body []byte) error {
			sigV4(r, body, ak, sk, st, region, "bedrock-agentcore", c.now())
			return nil
		}})
	if err != nil {
		return err
	}
	d := &j.res.Definition
	d.Source = src
	d.Name = str(doc, "agentRuntimeName")
	d.Description = str(doc, "description")
	d.Status = str(doc, "status")
	d.Version = str(doc, "agentRuntimeVersion")
	d.Protocol = firstOf(str(doc, "protocolConfiguration.serverProtocol"), "HTTP")
	if a := str(doc, "agentRuntimeArn"); a != "" {
		arn = a
	}
	if arn == "" {
		return errorf("the AgentCore answer for %s has no agentRuntimeArn", runtimeID)
	}
	d.Fields = map[string]string{"arn": arn, "runtime_id": runtimeID, "qualifier": qualifier}
	switch d.Protocol {
	case "MCP":
		return errors.New("this AgentCore runtime serves MCP: it is a tool server, not an agent. Import an HTTP or A2A runtime, or add it to a benchmark as a tool service")
	case "A2A":
		j.warn("This runtime speaks A2A. Heimdall calls its invocations route with a JSON prompt; if it only answers A2A JSON-RPC, connect it with the A2A agent card instead.")
	}
	if d.Status != "" && d.Status != "READY" {
		j.warn("The runtime is %s, not READY: runs fail until it is.", d.Status)
	}
	if obj(doc, "authorizerConfiguration.customJWTAuthorizer") != nil {
		j.warn("This runtime takes OAuth bearer tokens (a custom JWT authorizer), not SigV4: switch the agent's sign-in to a bearer token from your identity provider.")
	}
	invoke := "https://bedrock-agentcore." + region + ".amazonaws.com/runtimes/" + url.PathEscape(arn) + "/invocations?qualifier=" + url.QueryEscape(qualifier)
	auth := map[string]any{"type": "aws_sigv4", "region": region, "service": "bedrock-agentcore",
		"access_key_secret": j.secretName(SecretAccessKey), "secret_key_secret": j.secretName(SecretSecretKey)}
	if n := j.secretName(SecretSessionToken); n != "" {
		auth["session_token_secret"] = n
	}
	j.res.Adapter, j.res.Preset = "remote_http", "agentcore"
	j.res.Block = map[string]any{
		"url": invoke,
		"mapping": map[string]any{
			"preset":  "agentcore",
			"headers": map[string]string{AgentCoreSessionHeader: "heimdall-agentcore-session-{{session_id}}"},
			"body":    `{"prompt": "{{instruction}}", "session_id": "{{session_id}}"}`,
		},
		"auth": auth,
	}
	j.hint("sessions")
	j.warn("AgentCore passes your agent's own JSON through. Heimdall sends {\"prompt\": ...} and reads an answer field or a text body: check the request and answer path with Test my endpoint and adjust them if your entrypoint reads another field.")
	return nil
}

// ---- Salesforce Agentforce -------------------------------------------------

var (
	sfDomainRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*\.(my\.)?salesforce\.com$`)
	sfAgentIDRe = regexp.MustCompile(`^0Xx[A-Za-z0-9]{12}([A-Za-z0-9]{3})?$`)
)

// AgentforceAPI is the Agent API base (lane Y10's agentforce preset).
const AgentforceAPI = "https://api.salesforce.com/einstein/ai-agent/v1"

func importAgentforce(ctx context.Context, c *Client, j *job) error {
	dom := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(j.input("my_domain"), "https://"), "/"))
	if !sfDomainRe.MatchString(dom) {
		return errorf("My Domain host %q: want the host of your org, like acme.my.salesforce.com", dom)
	}
	if !sfAgentIDRe.MatchString(j.id) {
		return errorf("Agentforce agent id %q: want the 15 or 18 character BotDefinition id that starts with 0Xx", j.id)
	}
	tokenURL := "https://" + dom + "/services/oauth2/token"
	tok, instance, err := c.clientCredentials(ctx, tokenURL, j.secret(SecretClientID), j.secret(SecretClientSecret), "", "the Salesforce")
	if err != nil {
		return err
	}
	base := "https://" + dom
	if u, err := url.Parse(instance); err == nil && u.Scheme == "https" && sfDomainRe.MatchString(u.Host) {
		base = "https://" + u.Host
	}
	q := "SELECT Id, DeveloperName, MasterLabel, Description FROM BotDefinition WHERE Id = '" + j.id + "'"
	src := base + "/services/data/v62.0/query"
	doc, err := c.getJSON(ctx, call{method: http.MethodGet, url: src + "?q=" + url.QueryEscape(q),
		header: http.Header{"Authorization": {"Bearer " + tok}}, what: "the Salesforce BotDefinition query", secrets: append(j.secretValues(), tok)})
	if err != nil {
		return err
	}
	recs := list(doc, "records")
	if len(recs) == 0 {
		return errorf("no Agentforce agent with id %s in %s (the run-as user must be able to see it)", j.id, dom)
	}
	r := recs[0]
	d := &j.res.Definition
	d.Source = src
	d.Name = firstOf(str(r, "MasterLabel"), str(r, "DeveloperName"))
	d.Description = str(r, "Description")
	d.Fields = map[string]string{"developer_name": str(r, "DeveloperName"), "my_domain": dom}
	j.res.Adapter, j.res.Preset = "remote_http", "agentforce"
	j.res.Block = map[string]any{
		"url": AgentforceAPI + "/sessions/{{start.id}}/messages",
		"mapping": map[string]any{
			"preset":      "agentforce",
			"body":        `{"message": {"sequenceId": "{{session_index}}", "type": "Text", "text": "{{instruction}}"}}`,
			"answer_path": "messages[*].message",
			"start": map[string]any{"method": "POST", "url": AgentforceAPI + "/agents/" + j.id + "/sessions", "id_path": "sessionId",
				"body": `{"externalSessionKey": "{{uuid}}", "instanceConfig": {"endpoint": "https://` + dom + `"}, "streamingCapabilities": {"chunkTypes": ["Text"]}, "bypassUser": true}`},
			"end": map[string]any{"method": "DELETE", "url": AgentforceAPI + "/sessions/{{start.id}}", "headers": map[string]string{"x-session-end-reason": "UserRequest"}},
		},
		"auth": map[string]any{"type": "oauth2_client_credentials", "token_url": tokenURL,
			"client_id_secret": j.secretName(SecretClientID), "client_secret_secret": j.secretName(SecretClientSecret)},
	}
	j.hint("sessions")
	return nil
}

// ---- Microsoft Copilot Studio ---------------------------------------------

// DirectLineAPI is the Direct Line 3.0 base (lane Y10's copilot_studio
// preset).
const DirectLineAPI = "https://directline.botframework.com/v3/directline"

var (
	dataverseRe = regexp.MustCompile(`^https://[a-z0-9][a-z0-9-]*(\.[a-z0-9-]+)*\.dynamics\.com/?$`)
	guidRe      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	schemaRe    = regexp.MustCompile(`^[A-Za-z0-9_]{1,100}$`)
)

func importCopilotStudio(ctx context.Context, c *Client, j *job) error {
	if !guidRe.MatchString(j.id) && !schemaRe.MatchString(j.id) {
		return errorf("Copilot Studio agent %q: want the bot id (a GUID) or the schema name", j.id)
	}
	d := &j.res.Definition
	// The Direct Line secret is what runs use: prove it works first.
	tokURL := DirectLineAPI + "/tokens/generate"
	tdoc, err := c.getJSON(ctx, call{method: http.MethodPost, url: tokURL, body: []byte(`{}`),
		header: http.Header{"Authorization": {"Bearer " + j.secret(SecretDirectLine)}}, what: "Direct Line", secrets: j.secretValues()})
	if err != nil {
		return err
	}
	if str(tdoc, "token") == "" {
		return errors.New("Direct Line answered without a token: check the Direct Line secret")
	}
	d.Source = tokURL
	env := strings.TrimSpace(j.input("environment_url"))
	if env != "" {
		if !dataverseRe.MatchString(env) {
			return errorf("Dataverse environment URL %q: want https://<org>.crm.dynamics.com", env)
		}
		env = strings.TrimSuffix(env, "/")
		if err := c.checkUserURL(env); err != nil {
			return err
		}
		tenant := j.input("tenant_id")
		if !guidRe.MatchString(tenant) {
			return errors.New("reading the agent from Dataverse needs the Entra tenant id (a GUID)")
		}
		if j.secret(SecretClientID) == "" || j.secret(SecretClientSecret) == "" {
			return errors.New("reading the agent from Dataverse needs the Entra app client id and secret")
		}
		tok, _, err := c.clientCredentials(ctx, "https://login.microsoftonline.com/"+tenant+"/oauth2/v2.0/token",
			j.secret(SecretClientID), j.secret(SecretClientSecret), env+"/.default", "the Microsoft Entra")
		if err != nil {
			return err
		}
		src := env + "/api/data/v9.2/bots"
		q := url.Values{"$select": {"botid,name,schemaname,language,statecode"}, "$filter": {"schemaname eq '" + j.id + "'"}}
		if guidRe.MatchString(j.id) {
			q.Set("$filter", "botid eq "+j.id)
		}
		bdoc, err := c.getJSON(ctx, call{method: http.MethodGet, url: src + "?" + q.Encode(),
			header: http.Header{"Authorization": {"Bearer " + tok}}, what: "the Dataverse bots table", secrets: append(j.secretValues(), tok)})
		if err != nil {
			return err
		}
		rows := list(bdoc, "value")
		if len(rows) == 0 {
			return errorf("no Copilot Studio agent %s in %s", j.id, hostOnly(env))
		}
		r := rows[0]
		d.Source = src
		d.Name = str(r, "name")
		d.Language = str(r, "language")
		d.Fields = map[string]string{"bot_id": str(r, "botid"), "schema_name": str(r, "schemaname"), "environment": hostOnly(env)}
		if s := str(r, "statecode"); s != "" && s != "0" {
			j.warn("The agent is not active in Dataverse (statecode %s).", s)
		}
	} else {
		j.warn("Only the Direct Line secret was checked. Give the Dataverse environment URL, the tenant id and an Entra app to import the agent's name and language.")
	}
	j.res.Adapter, j.res.Preset = "remote_http", "copilot_studio"
	conv := DirectLineAPI + "/conversations"
	j.res.Block = map[string]any{
		"url": conv + "/{{start.id}}/activities",
		"mapping": map[string]any{
			"preset": "copilot_studio",
			"body":   `{"type": "message", "from": {"id": "heimdall-user", "role": "user"}, "text": "{{instruction}}"}`,
			"start":  map[string]any{"method": "POST", "url": conv, "id_path": "conversationId"},
			"poll": map[string]any{"url": conv + "/{{start.id}}/activities", "interval_s": 1, "reply_id_path": "id",
				"answer_path": "activities[?(@.replyToId=='{{reply.id}}')].text"},
		},
		"auth_secret": j.secretName(SecretDirectLine),
	}
	j.hint("sessions")
	return nil
}

// ---- Google Vertex AI Agent Engine ----------------------------------------

var (
	vertexNameRe   = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9]|[0-9]+)/locations/([a-z0-9-]+)/reasoningEngines/([0-9]{1,30})$`)
	vertexIDRe     = regexp.MustCompile(`^[0-9]{1,30}$`)
	gcpProjectRe   = regexp.MustCompile(`^([a-z][a-z0-9-]{4,28}[a-z0-9]|[0-9]+)$`)
	gcpRegionRe    = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]$`)
	vertexHostTmpl = "https://%s-aiplatform.googleapis.com/v1/"
)

func importVertex(ctx context.Context, c *Client, j *job) error {
	project, region, engine := j.input("project"), j.input("region"), ""
	if m := vertexNameRe.FindStringSubmatch(j.id); m != nil {
		if project != "" && project != m[1] || region != "" && region != m[2] {
			return errors.New("the project or region does not match the resource name")
		}
		project, region, engine = m[1], m[2], m[3]
	} else if vertexIDRe.MatchString(j.id) {
		engine = j.id
	} else {
		return errorf("%q is not a reasoning engine id or projects/<p>/locations/<r>/reasoningEngines/<id>", j.id)
	}
	tok, key, err := c.googleAccessToken(ctx, j.secret(SecretServiceAccount), CloudPlatformScope)
	if err != nil {
		return err
	}
	if project == "" {
		project = key.ProjectID
	}
	if !gcpProjectRe.MatchString(project) {
		return errorf("Vertex AI needs the project id (got %q)", project)
	}
	if region == "" {
		return errors.New("Vertex AI needs the region (or paste the full resource name)")
	}
	if !gcpRegionRe.MatchString(region) {
		return errorf("%q is not a Google Cloud region", region)
	}
	name := "projects/" + project + "/locations/" + region + "/reasoningEngines/" + engine
	src := fmt.Sprintf(vertexHostTmpl, region) + name
	doc, err := c.getJSON(ctx, call{method: http.MethodGet, url: src, header: http.Header{"Authorization": {"Bearer " + tok}},
		what: "the Vertex AI reasoning engine", secrets: append(j.secretValues(), tok)})
	if err != nil {
		return err
	}
	d := &j.res.Definition
	d.Source = src
	d.Name = str(doc, "displayName")
	d.Description = str(doc, "description")
	d.Protocol = str(doc, "spec.agentFramework")
	methods := map[string]string{}
	var names []string
	for _, m := range list(doc, "spec.classMethods") {
		n := str(m, "name")
		if n != "" {
			methods[n] = str(m, "api_mode")
			names = append(names, n)
		}
	}
	d.Fields = map[string]string{"resource": name, "methods": strings.Join(names, ", ")}
	base := fmt.Sprintf(vertexHostTmpl, region) + name
	var mapping map[string]any
	switch {
	case hasMethod(methods, "stream_query", "async_stream_query") && (d.Protocol == "google-adk" || !hasMethod(methods, "query")):
		cm := "stream_query"
		if _, ok := methods[cm]; !ok {
			cm = "async_stream_query"
		}
		in, _ := json.Marshal(map[string]any{"class_method": cm, "input": map[string]any{"user_id": "heimdall", "message": "{{instruction}}"}})
		mapping = map[string]any{"preset": "vertex", "body": string(in), "stream": "sse", "stream_path": "content.parts[*].text"}
		j.res.Block = map[string]any{"url": base + ":streamQuery?alt=sse"}
	case hasMethod(methods, "query") || len(methods) == 0:
		mapping = map[string]any{"preset": "vertex", "body": `{"class_method": "query", "input": {"input": "{{instruction}}"}}`, "answer_path": "output.output"}
		j.res.Block = map[string]any{"url": base + ":query"}
		if len(methods) == 0 {
			j.warn("The engine lists no methods; Heimdall assumes a query method that takes input.")
		}
	default:
		return errorf("the reasoning engine has no query or stream_query method (it has %s)", strings.Join(names, ", "))
	}
	j.res.Block["mapping"] = mapping
	j.res.Block["auth"] = map[string]any{"type": "gcp_access_token", "service_account_secret": j.secretName(SecretServiceAccount), "scope": CloudPlatformScope}
	j.res.Adapter, j.res.Preset = "remote_http", "vertex"
	j.warn("Vertex AI wants an OAuth access token (auth type gcp_access_token): the remote auth block must support it before runs can sign in.")
	return nil
}

func hasMethod(m map[string]string, names ...string) bool {
	for _, n := range names {
		if _, ok := m[n]; ok {
			return true
		}
	}
	return false
}

// ---- LangGraph Platform ----------------------------------------------------

var lgIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func importLangGraph(ctx context.Context, c *Client, j *job) error {
	raw := strings.TrimSpace(j.input("deployment_url"))
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return errorf("deployment URL %q: want https://<deployment host>", raw)
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/runs/wait"), "/")
	deploy := u.String()
	if err := c.checkUserURL(deploy); err != nil {
		return err
	}
	if !lgIDRe.MatchString(j.id) {
		return errorf("assistant or graph id %q: want letters, digits and _ . -", j.id)
	}
	var hdr http.Header
	if k := j.secret(SecretAPIKey); k != "" {
		hdr = http.Header{"X-Api-Key": {k}}
	}
	src := deploy + "/assistants/" + url.PathEscape(j.id)
	doc, err := c.getJSON(ctx, call{method: http.MethodGet, url: src, header: hdr, what: "the LangGraph assistant", secrets: j.secretValues()})
	if isStatus(err, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusBadRequest) {
		// Not an assistant id: look the graph up by its id.
		src = deploy + "/assistants/search"
		body, _ := json.Marshal(map[string]any{"graph_id": j.id, "limit": 1})
		raw, _, serr := c.do(ctx, call{method: http.MethodPost, url: src, header: hdr, body: body, what: "the LangGraph assistant search", secrets: j.secretValues()})
		if serr != nil {
			return serr
		}
		var hits []map[string]any
		if json.Unmarshal(raw, &hits) != nil || len(hits) == 0 {
			return errorf("no LangGraph assistant or graph named %s on %s", j.id, u.Host)
		}
		doc, err = hits[0], nil
	}
	if err != nil {
		return err
	}
	aid := str(doc, "assistant_id")
	if aid == "" {
		return errorf("the LangGraph answer for %s has no assistant_id", j.id)
	}
	d := &j.res.Definition
	d.Source = src
	d.Name = firstOf(str(doc, "name"), str(doc, "graph_id"))
	d.Description = firstOf(str(doc, "description"), str(doc, "metadata.description"))
	d.Version = str(doc, "version")
	d.Fields = map[string]string{"assistant_id": aid, "graph_id": str(doc, "graph_id")}
	if s, err := c.getJSON(ctx, call{method: http.MethodGet, url: deploy + "/assistants/" + url.PathEscape(aid) + "/schemas", header: hdr, what: "the LangGraph assistant schemas", secrets: j.secretValues()}); err == nil {
		props := obj(s, "input_schema.properties")
		var keys []string
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		d.Fields["input_fields"] = strings.Join(keys, ", ")
		if props != nil && props["messages"] == nil {
			j.warn("The graph's input has no messages field (it has %s): edit the request body so the task text lands in the field your graph reads.", strings.Join(keys, ", "))
		}
	}
	in, _ := json.Marshal(aid)
	block := map[string]any{
		"url": deploy + "/runs/wait",
		"mapping": map[string]any{
			"preset":      "langgraph",
			"body":        `{"assistant_id": ` + string(in) + `, "input": {"messages": [{"role": "user", "content": "{{instruction}}"}]}, "metadata": "{{metadata}}"}`,
			"answer_path": "messages[-1].content",
		},
	}
	if n := j.secretName(SecretAPIKey); n != "" {
		block["auth_secret"], block["auth_header"] = n, "x-api-key"
	}
	j.res.Adapter, j.res.Preset, j.res.Block = "remote_http", "langgraph", block
	return nil
}
