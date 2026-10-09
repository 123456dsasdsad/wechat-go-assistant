package weixin

import (
	"strings"
	"testing"
)

func TestForwardParserPreservesOrderProvenanceAndMissingFiles(t *testing.T) {
	raw := `<msg><appmsg><type>19</type><recorditem><![CDATA[<recordinfo><datalist><dataitem datatype="1"><datadesc>周五改摘要</datadesc><sourcename>导师</sourcename><sourcetime>今天10点</sourcetime></dataitem><dataitem datatype="8"><datatitle>代码.zip</datatitle></dataitem></datalist></recordinfo>]]></recorditem></appmsg></msg>`
	p, e := ParseAppMessage(raw)
	if e != nil || len(p.Sources) != 2 || p.Sources[0].Speaker != "导师" || p.Sources[0].Text != "周五改摘要" || len(p.Sources[1].Missing) != 1 || p.Text != "" {
		t.Fatal(p, e)
	}
	quote := `<msg><appmsg><type>57</type><title>2</title><refermsg><svrid>18446744073709551615</svrid><content>原文</content></refermsg></appmsg></msg>`
	p, e = ParseAppMessage(quote)
	if e != nil || p.Ref == nil || p.Ref.ServerID != "18446744073709551615" || p.Ref.Item.Text.Text != "原文" {
		t.Fatal(p, e)
	}
	if _, e = ParseAppMessage(strings.Repeat("x", 2<<20+1)); e == nil {
		t.Fatal("oversize accepted")
	}
	if _, e = ParseAppMessage(`<msg><appmsg><type>19</type><recorditem>bad xml</recorditem></appmsg></msg>`); e == nil {
		t.Fatal("invalid records accepted")
	}
}
