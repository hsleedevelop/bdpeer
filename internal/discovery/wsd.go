package discovery

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/multiformats/go-multiaddr"
)

const wsdMulticast = "239.255.255.250:3702"

var wsdHelloTmpl = template.Must(template.New("wsd").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope"
               xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
               xmlns:wsd="http://schemas.xmlsoap.org/ws/2005/04/discovery"
               xmlns:wsdp="http://schemas.xmlsoap.org/ws/2006/02/devprof">
  <soap:Header>
    <wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Hello</wsa:Action>
    <wsa:MessageID>urn:uuid:{{.MsgID}}</wsa:MessageID>
    <wsa:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</wsa:To>
  </soap:Header>
  <soap:Body>
    <wsd:Hello>
      <wsa:EndpointReference>
        <wsa:Address>urn:uuid:{{.DeviceID}}</wsa:Address>
      </wsa:EndpointReference>
      <wsd:Types>wsdp:Device</wsd:Types>
      <wsd:XAddrs>http://{{.Addr}}/bdpeer/{{.Nickname}}</wsd:XAddrs>
      <wsd:MetadataVersion>1</wsd:MetadataVersion>
    </wsd:Hello>
  </soap:Body>
</soap:Envelope>`))

func SendWSDHello(ctx context.Context, nickname string, port int) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return fmt.Errorf("wsd udp: %w", err)
	}
	defer conn.Close()

	dst, err := net.ResolveUDPAddr("udp4", wsdMulticast)
	if err != nil {
		return err
	}

	localIP := localIPv4()
	var buf bytes.Buffer
	_ = wsdHelloTmpl.Execute(&buf, map[string]string{
		"MsgID":    uuid.NewString(),
		"DeviceID": uuid.NewString(),
		"Addr":     fmt.Sprintf("%s:%d", localIP, port),
		"Nickname": nickname,
	})

	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.WriteToUDP(buf.Bytes(), dst)
	return err
}

func ListenWSD(ctx context.Context, mgr *Manager) error {
	addr, err := net.ResolveUDPAddr("udp4", "0.0.0.0:3702")
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("wsd listen 3702: %w (may need firewall rule on Windows)", err)
	}

	go func() {
		defer conn.Close()
		buf := make([]byte, 8192)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			body := string(buf[:n])
			if strings.Contains(body, "Hello") || strings.Contains(body, "ProbeMatch") {
				addrStr := extractXAddr(body, remote.String())
				nick := extractWSDNick(body)
				host, port, splitErr := net.SplitHostPort(addrStr)
				if addrStr != "" && splitErr == nil {
					ma, err := multiaddr.NewMultiaddr("/ip4/" + host + "/tcp/" + port)
					if err == nil {
						mgr.Notify(DiscoveredPeer{Nickname: nick, Addrs: []multiaddr.Multiaddr{ma}, Source: "wsd"})
					}
				}
			}
		}
	}()
	return nil
}

func extractXAddr(body, fallback string) string {
	start := strings.Index(body, "<wsd:XAddrs>")
	end := strings.Index(body, "</wsd:XAddrs>")
	if start < 0 || end < 0 {
		return fallback
	}
	raw := body[start+12 : end]
	raw = strings.TrimPrefix(raw, "http://")
	if idx := strings.Index(raw, "/"); idx >= 0 {
		raw = raw[:idx]
	}
	return raw
}

func extractWSDNick(body string) string {
	start := strings.Index(body, "/bdpeer/")
	if start < 0 {
		return "unknown"
	}
	end := strings.Index(body[start+8:], "<")
	if end < 0 {
		return body[start+8:]
	}
	return body[start+8 : start+8+end]
}

func localIPv4() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
