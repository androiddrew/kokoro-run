package textprep

import "testing"

func TestText(t *testing.T) {
	both := Options{Markdown: true, Normalize: true}
	for _, c := range []struct {
		in   string
		o    Options
		want string
	}{
		{"Meet at 10:30 AM on May 5, 2026.", Options{}, "Meet at 10:30 AM on May 5, 2026."},
		{"Meet at 10:30 AM on May 5, 2026.", Options{Normalize: true}, "Meet at ten thirty AM on May fifth, twenty twenty-six."},
		{"**Note:** it's [Kubernetes](/kˌubəɹnˈɛtiz/) v1.2.3, not `kubectl`.", both, "Note: it's [Kubernetes](/kˌubəɹnˈɛtiz/) v one point two point three, not kubectl."},
		{"## Steps\n\n1. Read pages 31-35\n2. It weighs 2.5 kg", both, "Steps. Read pages thirty-one to thirty-five. It weighs two point five kilograms."},
		{"Email hello@example.com", both, "Email H E L L O at E X A M P L E dot C O M"},
	} {
		if got := Text(c.in, c.o); got != c.want {
			t.Errorf("Text(%q, %+v)\n got: %q\nwant: %q", c.in, c.o, got, c.want)
		}
	}
}
