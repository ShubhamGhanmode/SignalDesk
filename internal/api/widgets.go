package api

import (
	"fmt"
	"strings"
)

type WidgetDefinition struct {
	Type string `json:"type"`
	Title string `json:"title"`
	Version int `json:"version"`
	Roles []string `json:"roles"`
	Fields map[string]string `json:"fields"`
}
type Widget struct { ID string `json:"id"`; Type string `json:"type"`; Version int `json:"version"`; Config map[string]string `json:"config"` }
type Workspace struct { Version int `json:"version"`; Widgets []Widget `json:"widgets"` }
var registry=[]WidgetDefinition{
	{Type:"recent-events",Title:"Recent events",Version:1,Roles:[]string{"analyst"},Fields:map[string]string{"domain":"optional: identity | endpoint | cloud"}},
	{Type:"severity-breakdown",Title:"Severity breakdown",Version:1,Roles:[]string{"analyst","manager"},Fields:map[string]string{}},
	{Type:"domain-breakdown",Title:"Domain activity",Version:1,Roles:[]string{"analyst","manager"},Fields:map[string]string{}},
	{Type:"entity-timeline",Title:"Entity timeline",Version:1,Roles:[]string{"analyst"},Fields:map[string]string{"entity_id":"required: entity identifier"}},
}
func definitions(role string) []WidgetDefinition { out:=[]WidgetDefinition{}; for _,d:=range registry { for _,r:=range d.Roles { if r==role { out=append(out,d); break } } }; return out }
func defaultWorkspace(role string) Workspace {
	w:=Workspace{Version:1,Widgets:[]Widget{{"severity","severity-breakdown",1,map[string]string{}},{"domains","domain-breakdown",1,map[string]string{}}}}
	if role=="analyst" { w.Widgets=append(w.Widgets,Widget{"recent","recent-events",1,map[string]string{}}) }; return w
}
func (w Workspace) Validate(role string) error {
	if w.Version!=1 || len(w.Widgets)>8 { return fmt.Errorf("workspace version must be 1; maximum eight widgets") }
	seen:=map[string]bool{}
	for _,widget:=range w.Widgets {
		if widget.ID=="" || len(widget.ID)>64 || seen[widget.ID] { return fmt.Errorf("widget IDs must be unique and 1..64 characters") }; seen[widget.ID]=true
		var definition *WidgetDefinition
		for _,d:=range definitions(role) { if d.Type==widget.Type && d.Version==widget.Version { copy:=d; definition=&copy; break } }
		if definition==nil { return fmt.Errorf("unknown or unauthorized widget: %s",widget.Type) }
		for key,value:=range widget.Config { if _,ok:=definition.Fields[key];!ok || len(value)>200 { return fmt.Errorf("invalid configuration for %s",widget.Type) } }
		if widget.Type=="entity-timeline" && strings.TrimSpace(widget.Config["entity_id"])=="" { return fmt.Errorf("entity-timeline requires entity_id") }
		if widget.Type=="recent-events" { d:=widget.Config["domain"]; if d!="" && d!="identity" && d!="endpoint" && d!="cloud" { return fmt.Errorf("invalid widget domain") } }
	}
	return nil
}
