package server

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func pageScriptBetween(t *testing.T, page, begin, end string) string {
	t.Helper()
	start := strings.Index(page, begin)
	if start < 0 {
		t.Fatalf("missing script start %q", begin)
	}
	stop := strings.Index(page[start:], end)
	if stop < 0 {
		t.Fatalf("missing script end %q", end)
	}
	return page[start : start+stop]
}

func TestSMSOnlyPageNeverPollsNetworkFeatures(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	page := notificationPage(t)
	functions := pageScriptBetween(t, page, "async function loadMetered(){", "function renderMetered(){") +
		pageScriptBetween(t, page, "async function pollTraffic(){", "/* ---------------- sms ---------------- */") +
		pageScriptBetween(t, page, "let dataEnabled=null,", "$(\"databtn\").addEventListener")
	boot := pageScriptBetween(t, page, "/* ---------------- boot ---------------- */", "</script>")
	for _, smsOnly := range []bool{true, false} {
		mode, _ := json.Marshal(smsOnly)
		script := `
const assert=require("assert");
const SMS_ONLY=` + string(mode) + `;
let viewSim=null;
const elements=new Map();
const $=id=>{if(!elements.has(id))elements.set(id,{hidden:false,textContent:"",classList:{remove(){},add(){},toggle(){}}});return elements.get(id)};
const requests=[], timers=[], actions=[];
async function fetch(path){requests.push(path);throw Error("fixture: no I/O")}
const setInterval=(fn,ms)=>timers.push(fn.name);
const loadSims=async()=>{};
const loadInbox=()=>actions.push("inbox");
const loadNotificationSettings=()=>actions.push("notifications");
const loadLock=()=>{};
const pollStatus=()=>{};
const renderMetered=()=>{};
` + functions + `
async function run(){
` + boot + `
await Promise.resolve();
await pollTraffic(); await loadMetered();
assert(actions.includes("inbox"),"inbox startup lost");
assert(actions.includes("notifications"),"notification startup lost");
if(SMS_ONLY){
  assert(!requests.some(p=>p.startsWith("/api/traffic")||p.startsWith("/api/metered")),JSON.stringify(requests));
  assert(!timers.includes("pollTraffic")&&!timers.includes("loadMetered"),JSON.stringify(timers));
  for(const [state,label] of [["off","已核验关闭"],["pending","等待模组"],["error","核验失败"]]){
    dataGuardState=state; renderData();
    assert($("sms-guard-status").textContent.includes(label));
    assert($("databtn").hidden);
  }
}else{
  assert(requests.some(p=>p.startsWith("/api/traffic")),"legacy traffic lost");
  assert(timers.includes("pollTraffic")&&timers.includes("loadMetered"),"legacy timers lost");
}
}
run().catch(e=>{console.error(e);process.exitCode=1});`
		if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
			t.Fatalf("smsOnly=%v: %v: %s", smsOnly, err, out)
		}
	}
}
