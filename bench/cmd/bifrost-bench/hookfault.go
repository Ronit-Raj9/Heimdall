package main

import (
	"encoding/json"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/hookfault"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

func init() {
	register("hookfault", "serve or freeze a private per-run adapter fault controller", runHookFault)
}

func runHookFault(args []string) int {
	fs := flag.NewFlagSet("hookfault", flag.ContinueOnError)
	cfgPath := fs.String("config", "/cfg/runtime.json", "private controller configuration")
	action := fs.String("action", "serve", "serve, health or freeze")
	sessions := fs.String("sessions", "", "JSON array of executed sessions")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *action == "health" {
		if _, _, err := fasthttp.Get(nil, "http://127.0.0.1:9300/healthz"); err != nil {
			return fail("hook controller health: %v", err)
		}
		return exitOK
	}
	raw, err := os.ReadFile(*cfgPath)
	if err != nil {
		return fail("read hook configuration: %v", err)
	}
	var cfg schemas.AdapterFaultConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fail("parse hook configuration: %v", err)
	}
	if *action == "freeze" {
		var ids []string
		if err := json.Unmarshal([]byte(*sessions), &ids); err != nil {
			return fail("parse executed sessions: %v", err)
		}
		body, err := json.Marshal(struct {
			Sessions []string `json:"sessions"`
		}{ids})
		if err != nil {
			return fail("encode freeze: %v", err)
		}
		req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)
		req.SetRequestURI("http://127.0.0.1:9300/admin/freeze")
		req.Header.SetMethod("POST")
		req.Header.Set("Authorization", "Bearer "+cfg.AdminToken)
		req.SetBody(body)
		if err := fasthttp.DoTimeout(req, resp, 10*time.Second); err != nil {
			return fail("freeze hook controller: %v", err)
		}
		if resp.StatusCode() != 200 {
			return fail("freeze hook controller: status %d", resp.StatusCode())
		}
		if err := os.WriteFile("/ledger/adapter.fault.json", resp.Body(), 0600); err != nil {
			return fail("write frozen hook evidence: %v", err)
		}
		return exitOK
	}
	if *action != "serve" {
		return fail("unknown hookfault action %s", *action)
	}
	if err := os.MkdirAll("/ledger", 0700); err != nil {
		return fail("prepare hook ledger: %v", err)
	}
	ledger, err := os.OpenFile("/ledger/adapter.fault.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fail("open hook ledger: %v", err)
	}
	defer ledger.Close()
	c, err := hookfault.New(cfg, ledger)
	if err != nil {
		return fail("configure hook controller: %v", err)
	}
	srv := &fasthttp.Server{Handler: c.Handle, MaxRequestBodySize: utils.AdapterFaultMaxControl, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, Name: strings.Join([]string{"bench", "hooks"}, "-")}
	if err := srv.ListenAndServe(":9300"); err != nil {
		return fail("serve hook controller: %v", err)
	}
	return exitOK
}
