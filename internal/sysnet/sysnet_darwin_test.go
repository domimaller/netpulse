//go:build darwin

package sysnet

import "testing"

func TestParseNetstat(t *testing.T) {
	out := []byte(`Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
en0        1500  <Link#11>   aa:bb:cc:dd:ee:ff  1000     0    123456     900     0     654321     0
en0        1500  192.168.1     192.168.1.20     1000     -    999999     900     -     888888     -
`)
	c, err := parseNetstat(out, "en0")
	if err != nil {
		t.Fatal(err)
	}
	if c.RxBytes != 123456 || c.TxBytes != 654321 {
		t.Fatalf("got %+v", c)
	}
	utun := []byte(`Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
utun4      1380  <Link#20>                          50     0      5000       40     0       4000     0
`)
	c, err = parseNetstat(utun, "utun4")
	if err != nil || c.RxBytes != 5000 || c.TxBytes != 4000 {
		t.Fatalf("utun got %+v %v", c, err)
	}
}

func TestParseRouteGet(t *testing.T) {
	r := parseRouteGet([]byte("   route to: default\ndestination: default\n       mask: default\n    gateway: 192.168.1.1\n  interface: en0\n"))
	if r.Gateway != "192.168.1.1" || r.Interface != "en0" {
		t.Fatalf("got %+v", r)
	}
}
