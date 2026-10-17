package server

import (
	"os/exec"
	"strings"
	"testing"
)

func TestHeartbeatPageDoesNotOverwriteEditingOrPersistSecret(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	page := notificationPage(t)
	script := pageScriptBetween(t, page, "/* ---------------- heartbeat ---------------- */", "/* ---------------- notification settings ---------------- */")
	for _, bad := range []string{"localStorage", "sessionStorage", "innerHTML"} {
		if strings.Contains(script, bad) {
			t.Fatalf("unsafe heartbeat UI: %s", bad)
		}
	}
	if !strings.Contains(page, `id="hb-secret" type="password"`) {
		t.Fatal("missing secret password")
	}
	js := `const assert=require('assert');const fields={};const $=id=>fields[id]||(fields[id]={value:'',checked:false,disabled:false,textContent:'',addEventListener(){}});let requests=[];let fail=false;
 const status={config:{enabled:false,device_id:'stored',name:'router',endpoint:'https://ecs.example/v1/heartbeat',interval_seconds:600},report:{healthy:true,checks:{at:true}},secret_configured:true};
 global.fetch=async(p,o)=>{requests.push([p,o]);return {ok:!fail,json:async()=>status}};
 global.setInterval=()=>{};
 ` + script + `
 async function verify(){await loadHeartbeatSettings();$('hb-device').value='editing';await loadHeartbeatSettings();assert.equal($('hb-device').value,'editing');$('hb-secret').value='private';await heartbeatSave({preventDefault(){}});assert.equal($('hb-secret').value,'');assert(requests.some(x=>x[0]==='/api/heartbeat/config'));fail=true;await heartbeatAction('send');assert($('hb-note').textContent.includes('失败'));}
 verify().catch(e=>{console.error(e);process.exitCode=1});`
	if out, err := exec.Command("node", "-e", js).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}
