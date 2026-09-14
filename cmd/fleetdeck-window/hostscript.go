//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// hostScript is the user script each web view gets before its page loads: the
// host object web/js/host.js reads, and, for a surface, the bindings the board
// gets from webview_go, carried over the surface's own message handler.
//
// A binding call posts {id, name, args} to the handler named fleetdeck and
// waits on a promise; the window answers through host._reply(id, ok, value).
// hostOnStand marks the host object stand: true, for a window on a CI stand
// (standSocketEnv): the board then says in the window's log what a screenshot
// alone cannot prove (web/js/standreport.js). Never set in a person's window.
var hostOnStand bool

// hostStandOpen is what a stand's board opens as it loads (standOpenEnv):
// "newcard" or nothing. Read only when hostOnStand.
var hostStandOpen string

func hostScript(surface string, glass glassMode, bindings []string) string {
	switch surface {
	case "board", "orchestrator", "sessions":
	default:
		panic(fmt.Sprintf("hostScript: unknown surface %q", surface))
	}
	if bindings == nil {
		bindings = []string{}
	}
	names, _ := json.Marshal(bindings)
	var b strings.Builder
	b.WriteString("(function(){if(window.fleetdeckHost)return;")
	fmt.Fprintf(&b, `var host={version:1,surface:%q,glass:%q,receive:function(){}};`, surface, string(glass))
	if hostOnStand {
		b.WriteString("host.stand=true;")
		if hostStandOpen != "" {
			fmt.Fprintf(&b, "host.standOpen=%q;", hostStandOpen)
		}
	}
	b.WriteString("var pending={},next=0;")
	b.WriteString("host._reply=function(id,ok,value){var p=pending[id];if(!p)return;delete pending[id];if(ok)p.resolve(value);else p.reject(new Error(value));};")
	fmt.Fprintf(&b, "%s.forEach(function(name){window[name]=function(args){return new Promise(function(resolve,reject){var id=++next;pending[id]={resolve:resolve,reject:reject};window.webkit.messageHandlers.fleetdeck.postMessage(JSON.stringify({id:id,name:name,args:args===undefined?null:args}));});};});", names)
	b.WriteString("window.fleetdeckHost=host;})();")
	return b.String()
}
