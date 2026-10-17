package server

import (
	"os/exec"
	"strings"
	"testing"
)

func TestMeteredPageHidesUnsupportedLinuxControl(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	page := notificationPage(t)
	start := strings.Index(page, "function renderMetered(){")
	if start < 0 {
		t.Fatal("metered render function missing")
	}
	end := strings.Index(page[start:], `$("meteredbtn").addEventListener`)
	if end < 0 {
		t.Fatal("metered render function missing")
	}
	script := `const assert=require("assert");
let button={hidden:false,classList:{toggle(){} }};
const $=()=>button;
const isCurrent=()=>true;
let metered={supported:false,enabled:true,applied:false};
` + page[start:start+end] + `
renderMetered();
assert.equal(button.hidden,true,"Linux must not show a permanently pending macOS switch");
metered={supported:true,enabled:true,applied:true};
renderMetered();
assert.equal(button.hidden,false,"macOS switch must remain visible");
assert.equal(button.textContent,"low data: on");
`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
