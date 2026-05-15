//go:build darwin

package discovery

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreBluetooth -framework Foundation
#include "ble_corebluetooth.h"
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"sync"
	"unsafe"
)

// bleCallbacks holds per-session callbacks set before ble_start.
var bleCallbacks struct {
	sync.Mutex
	onPeerFound         func(nickname, peerUUID string)
	onSDPReceived       func(peerUUID, sdp string, isOffer bool)
	onCentralSubscribed func(centralUUID string)
	onDataReceived      func(peerUUID string, data []byte)
}

// SetBLECallbacks registers Go handlers called from CoreBluetooth.
// Must be called before StartBLE.
func SetBLECallbacks(
	onPeer func(nickname, peerUUID string),
	onSDP func(peerUUID, sdp string, isOffer bool),
	onCentral func(centralUUID string),
) {
	bleCallbacks.Lock()
	defer bleCallbacks.Unlock()
	bleCallbacks.onPeerFound = onPeer
	bleCallbacks.onSDPReceived = onSDP
	bleCallbacks.onCentralSubscribed = onCentral
}

// SetBLEDataCallback registers the handler for inbound BLE data frames.
// Call before StartBLE.
func SetBLEDataCallback(onData func(peerUUID string, data []byte)) {
	bleCallbacks.Lock()
	defer bleCallbacks.Unlock()
	bleCallbacks.onDataReceived = onData
}

// BLEPeripheralSendDataTo sends data to a specific central (peripheral→central notify).
func BLEPeripheralSendDataTo(centralUUID string, data []byte) {
	if len(data) == 0 {
		return
	}
	cu := C.CString(centralUUID)
	defer C.free(unsafe.Pointer(cu))
	C.ble_peripheral_send_data_to(cu, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)))
}

// BLECentralSendData sends data to a specific peripheral (central→peripheral write).
func BLECentralSendData(peripheralUUID string, data []byte) {
	if len(data) == 0 {
		return
	}
	pu := C.CString(peripheralUUID)
	defer C.free(unsafe.Pointer(pu))
	C.ble_central_send_data(pu, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)))
}

// BLEPeripheralSendSDP sends an SDP answer to all connected centrals.
func BLEPeripheralSendSDP(sdp string) {
	cs := C.CString(sdp)
	defer C.free(unsafe.Pointer(cs))
	C.ble_peripheral_send_sdp(cs, 'A')
}

// BLECentralSendSDP sends an SDP offer to a specific peripheral.
func BLECentralSendSDP(peerUUID, sdp string) {
	cu := C.CString(peerUUID)
	cs := C.CString(sdp)
	defer C.free(unsafe.Pointer(cu))
	defer C.free(unsafe.Pointer(cs))
	C.ble_central_send_sdp(cu, cs, 'O')
}

// StartBLE starts CoreBluetooth peripheral advertising + central scanning.
// Blocks until ctx is cancelled.
func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
	// Wire default discovery callback if no custom one is set.
	bleCallbacks.Lock()
	if bleCallbacks.onPeerFound == nil {
		bleCallbacks.onPeerFound = func(nick, _ string) {
			mgr.Notify(DiscoveredPeer{Nickname: nick, Source: "ble"})
		}
	}
	bleCallbacks.Unlock()

	cn := C.CString(nickname)
	defer C.free(unsafe.Pointer(cn))

	// ble_start blocks (runs NSRunLoop). Run on a separate OS thread.
	done := make(chan struct{})
	go func() {
		defer close(done)
		C.ble_start(cn)
	}()

	select {
	case <-ctx.Done():
		C.ble_stop()
		<-done
	case <-done:
	}
	return nil
}

// ── CGo export callbacks ──────────────────────────────────────────────────────

//export go_ble_peer_found
func go_ble_peer_found(nickname *C.char, peerUUID *C.char) {
	bleCallbacks.Lock()
	cb := bleCallbacks.onPeerFound
	bleCallbacks.Unlock()
	if cb != nil {
		cb(C.GoString(nickname), C.GoString(peerUUID))
	}
}

//export go_ble_sdp_received
func go_ble_sdp_received(peerUUID *C.char, sdp *C.char, isOffer C.int) {
	bleCallbacks.Lock()
	cb := bleCallbacks.onSDPReceived
	bleCallbacks.Unlock()
	if cb != nil {
		cb(C.GoString(peerUUID), C.GoString(sdp), isOffer == 1)
	}
}

//export go_ble_central_subscribed
func go_ble_central_subscribed(centralUUID *C.char) {
	bleCallbacks.Lock()
	cb := bleCallbacks.onCentralSubscribed
	bleCallbacks.Unlock()
	if cb != nil {
		cb(C.GoString(centralUUID))
	}
}

//export go_ble_data_received
func go_ble_data_received(peerUUID *C.char, data *C.uint8_t, length C.int) {
	bleCallbacks.Lock()
	cb := bleCallbacks.onDataReceived
	bleCallbacks.Unlock()
	if cb == nil || length == 0 {
		return
	}
	buf := C.GoBytes(unsafe.Pointer(data), length)
	cb(C.GoString(peerUUID), buf)
}
