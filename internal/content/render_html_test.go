package content

import "testing"

// Blank lines start paragraphs, single line breaks stay breaks, and the
// campaign URL is the one link.
func TestRenderEmailHTMLLayout(t *testing.T) {
	got, err := RenderEmailHTML("Merhaba {{first_name}},\n\nSatır 1\nSatır 2\n\nBak: {{campaign_url}}", RenderContext{FirstName: "Ali", CampaignURL: testURL})
	if err != nil {
		t.Fatal(err)
	}
	want := "<!doctype html>\n<html><body>\n<p>Merhaba Ali,</p>\n<p>Satır 1<br>Satır 2</p>\n<p>Bak: <a href=\"" + testURL + "\">" + testURL + "</a></p>\n</body></html>\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
