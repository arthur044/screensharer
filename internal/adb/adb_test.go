package adb

import (
	"reflect"
	"testing"
)

func TestParseDevices(t *testing.T) {
	out := `* daemon not running; starting now at tcp:5037
* daemon started successfully
List of devices attached
R58M12345	device usb:1-1 product:a51 model:SM_A515F device:a51 transport_id:1
ZY22ABCD	unauthorized usb:1-2 transport_id:2
0123456789	no permissions (missing udev rules? user is in the plugdev group); see [http://developer.android.com/tools/device.html] usb:3-1 transport_id:3
192.168.0.15:37099	device product:panther model:Pixel_7 device:panther transport_id:4
adb-26011FDH2000AB-xyz._adb-tls-connect._tcp	offline transport_id:5

`
	got := ParseDevices(out)
	want := []Device{
		{Serial: "R58M12345", State: "device", Model: "SM A515F"},
		{Serial: "ZY22ABCD", State: "unauthorized"},
		{Serial: "0123456789", State: "no permissions"},
		{Serial: "192.168.0.15:37099", State: "device", Model: "Pixel 7", WiFi: true},
		{Serial: "adb-26011FDH2000AB-xyz._adb-tls-connect._tcp", State: "offline", WiFi: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseMDNS(t *testing.T) {
	out := "List of discovered mdns services\n" +
		"adb-26011FDH2000AB-xyz\t_adb-tls-connect._tcp\t192.168.0.15:37099\n" +
		"adb-26011FDH2000AB-xyz\t_adb-tls-pairing._tcp\t192.168.0.15:41235\n" +
		"other\t_http._tcp\t192.168.0.2:80\n"
	got := ParseMDNS(out)
	want := []MDNSService{
		{Name: "adb-26011FDH2000AB-xyz", Type: "connect", Address: "192.168.0.15:37099"},
		{Name: "adb-26011FDH2000AB-xyz", Type: "pairing", Address: "192.168.0.15:41235"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}
