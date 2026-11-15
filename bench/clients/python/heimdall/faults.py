"""Opt-in adapter fault hooks for benchmark agents.

The controller is authoritative for the injected decision. This module only
reports callback boundaries and fails closed when a configured controller is
unreachable.
"""
import asyncio
import inspect
import json
import os
import time
import urllib.request
import uuid
from typing import Any, Callable, Optional

class FaultControlError(RuntimeError): pass
class InjectedFaultError(RuntimeError): pass
class InjectedTimeoutError(TimeoutError): pass

class FaultSession:
    def __init__(self, session: Optional[str] = None):
        self.url=os.getenv("BENCH_FAULT_URL"); self.token=os.getenv("BENCH_FAULT_TOKEN"); self.run=os.getenv("BENCH_FAULT_RUN_ID"); self.session=session or os.getenv("BENCH_FAULT_SESSION") or "default"; self.closed=False
        try: self.hooks=json.loads(os.getenv("BENCH_FAULT_HOOKS","[]"))
        except json.JSONDecodeError as e: raise FaultControlError("invalid BENCH_FAULT_HOOKS") from e
        if bool(self.url) != bool(self.token) or (self.url and not self.run): raise FaultControlError("adapter fault runtime is partially configured")
        if self.url: self._post("register", {"version":"1","run_id":self.run,"session":self.session,"phase":"register","hooks":self.hooks})
    def _post(self,path,payload):
        if not self.url: return {}
        req=urllib.request.Request(self.url+"/v1/"+path, data=json.dumps(payload).encode(), headers={"Content-Type":"application/json","Authorization":"Bearer "+self.token,"X-Bench-Run":self.run})
        try:
            with urllib.request.urlopen(req, timeout=5) as r: return json.loads(r.read())
        except Exception as e: raise FaultControlError(f"adapter fault control {path}: {e}") from e
    def _decision(self,kind,name):
        op=uuid.uuid4().hex
        d=self._post("before", {"version":"1","run_id":self.run,"session":self.session,"operation_id":op,"kind":kind,"name":name,"phase":"before"})
        return op,d.get("mode","pass") if d else "pass",d
    def invoke(self,kind:str,name:str,fn:Callable[[],Any])->Any:
        if not self.url: return fn()
        op,mode,d=self._decision(kind,name)
        if mode == "error": self._post("ack", {"version":"1","run_id":self.run,"session":self.session,"operation_id":op,"kind":kind,"name":name,"phase":"applied","success":True}); raise InjectedFaultError(d.get("message","injected adapter fault"))
        if mode == "timeout":
            self._post("ack", {"version":"1","run_id":self.run,"session":self.session,"operation_id":op,"kind":kind,"name":name,"phase":"applied","success":True}); time.sleep(d.get("timeout_ms",1)/1000); raise InjectedTimeoutError(d.get("message","injected adapter timeout"))
        if mode == "replace":
            self._post("ack", {"version":"1","run_id":self.run,"session":self.session,"operation_id":op,"kind":kind,"name":name,"phase":"applied","success":True}); return json.loads(d.get("result") or "null") if isinstance(d.get("result"),str) else d.get("result")
        try: out=fn(); ok=True
        except Exception: ok=False; raise
        finally: self._post("ack", {"version":"1","run_id":self.run,"session":self.session,"operation_id":op,"kind":kind,"name":name,"phase":"completed","success":ok})
        return out
    async def ainvoke(self,kind:str,name:str,fn:Callable[[],Any])->Any:
        if not self.url:
            out = fn()
            return await out if inspect.isawaitable(out) else out
        op,mode,d=self._decision(kind,name)
        if mode in ("error","timeout"):
            self._post("ack", {"version":"1","run_id":self.run,"session":self.session,"operation_id":op,"phase":"applied","success":True})
            if mode=="timeout": await asyncio.sleep(d.get("timeout_ms",1)/1000); raise InjectedTimeoutError(d.get("message","injected adapter timeout"))
            raise InjectedFaultError(d.get("message","injected adapter fault"))
        if mode=="replace": self._post("ack", {"version":"1","run_id":self.run,"session":self.session,"operation_id":op,"phase":"applied","success":True}); return json.loads(d.get("result") or "null") if isinstance(d.get("result"),str) else d.get("result")
        try:
            out = fn()
            if inspect.isawaitable(out): out = await out
            ok=True
        except Exception: ok=False; raise
        finally: self._post("ack", {"version":"1","run_id":self.run,"session":self.session,"operation_id":op,"phase":"completed","success":ok})
        return out
    def close(self):
        if self.url and not self.closed: self._post("finish", {"version":"1","run_id":self.run,"session":self.session,"phase":"finish","healthy":True}); self.closed=True
    def __enter__(self): return self
    def __exit__(self,*_): self.close()
    async def __aenter__(self): return self
    async def __aexit__(self,*_): self.close()

def hook(kind:str,name:str,fn:Callable[[],Any],session:Optional[FaultSession]=None)->Any:
    """Execute one instrumented operation, applying the controller decision."""
    own=session or FaultSession();
    try: return own.invoke(kind,name,fn)
    finally:
        if session is None: own.close()
