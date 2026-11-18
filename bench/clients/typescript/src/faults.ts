export class FaultControlError extends Error {}
export class InjectedFaultError extends Error {}
export class InjectedTimeoutError extends Error {}
type Decision={mode?:string,message?:string,timeout_ms?:number,result?:unknown};
const env: Record<string,string|undefined> = ((globalThis as any).process?.env ?? {}) as Record<string,string|undefined>;
/** A run with realism neutral names the variables AGENT_HOOK_* (docs/eval-awareness.md); read first. */
const hookEnv = (suffix: string): string|undefined => env["AGENT_HOOK_"+suffix] || env["BENCH_FAULT_"+suffix];

/** Scoped adapter hook client. It is pass-through when no runtime is configured. */
export class FaultSession {
  private url=hookEnv("URL"); private token=hookEnv("TOKEN"); private run=hookEnv("RUN_ID"); private session: string; private closed=false; private opened=false; private hooks: unknown[];
  constructor(session?:string){ this.session=session||hookEnv("SESSION")||"default"; try {this.hooks=JSON.parse(hookEnv("HOOKS")||"[]");} catch(e) {throw new FaultControlError("invalid BENCH_FAULT_HOOKS");} if(!!this.url!==!!this.token || (!!this.url&&!this.run)) throw new FaultControlError("adapter fault runtime is partially configured"); }
  async open(){if(this.url&&!this.opened){await this.post("register",{version:"1",run_id:this.run,session:this.session,phase:"register",hooks:this.hooks});this.opened=true;} return this;}
  private async post(path:string,body:unknown):Promise<any>{if(!this.url)return{}; const r=await fetch(this.url+"/v1/"+path,{method:"POST",headers:{"content-type":"application/json",authorization:`Bearer ${this.token}`,"X-Bench-Run":this.run!},body:JSON.stringify(body)});if(!r.ok)throw new FaultControlError(`adapter fault control ${path}: ${r.status}`);return r.json();}
  async invoke<T>(kind:string,name:string,fn:()=>Promise<T>|T):Promise<T>{if(!this.url)return fn();await this.open();const op=crypto.randomUUID();const d:Decision=await this.post("before",{version:"1",run_id:this.run,session:this.session,operation_id:op,kind,name,phase:"before"});const ack=(phase:string,success=true)=>this.post("ack",{version:"1",run_id:this.run,session:this.session,operation_id:op,kind,name,phase,success});if(d.mode==="error"){await ack("applied");throw new InjectedFaultError(d.message||"injected adapter fault");}if(d.mode==="timeout"){await ack("applied");await new Promise(r=>setTimeout(r,d.timeout_ms||1));throw new InjectedTimeoutError(d.message||"injected adapter timeout");}if(d.mode==="replace"){await ack("applied");return d.result as T;}try{const out=await fn();await ack("completed");return out;}catch(e){await ack("completed",false);throw e;}}
  async close(){if(this.url&&!this.closed){await this.post("finish",{version:"1",run_id:this.run,session:this.session,phase:"finish",healthy:true});this.closed=true;}}
}
export async function hook<T>(kind:string,name:string,fn:()=>Promise<T>|T,session?:FaultSession):Promise<T>{const s=session||await new FaultSession().open();try{return await s.invoke(kind,name,fn)}finally{if(!session)await s.close();}}
