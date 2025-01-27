package methods

import (
	"context"
	"runtime/cgo"
	"time"
	"unsafe"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/stellar/go/support/log"
	"github.com/stellar/go/xdr"

	"github.com/stellar/soroban-tools/cmd/soroban-rpc/internal/db"
)

/*
#include "../../lib/preflight.h"
#include <stdlib.h>
// This assumes that the Rust compiler should be using a -gnu target (i.e. MinGW compiler) in Windows
// (I (fons) am not even sure if CGo supports MSVC, see https://github.com/golang/go/issues/20982)
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/../../../../target/x86_64-pc-windows-gnu/release-with-panic-unwind/ -lpreflight -ldl -lm -static -lws2_32 -lbcrypt -luserenv
// You cannot compile with -static in macOS (and it's not worth it in Linux, at least with glibc)
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/../../../../target/x86_64-apple-darwin/release-with-panic-unwind/ -lpreflight -ldl -lm
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/../../../../target/aarch64-apple-darwin/release-with-panic-unwind/ -lpreflight -ldl -lm
// In Linux, at least for now, we will be dynamically linking glibc. See https://github.com/2opremio/soroban-go-rust-preflight-poc/issues/3 for details
// I (fons) did try linking statically against musl but it caused problems catching (unwinding) Rust panics.
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/../../../../target/x86_64-unknown-linux-gnu/release-with-panic-unwind/ -lpreflight -ldl -lm
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/../../../../target/aarch64-unknown-linux-gnu/release-with-panic-unwind/ -lpreflight -ldl -lm
*/
import "C"

type snapshotSourceHandle struct {
	readTx db.LedgerEntryReadTx
	logger *log.Entry
}

// SnapshotSourceGet takes a LedgerKey XDR in base64 string and returns its matching LedgerEntry XDR in base64 string
// It's used by the Rust preflight code to obtain ledger entries.
//
//export SnapshotSourceGet
func SnapshotSourceGet(handle C.uintptr_t, cLedgerKey *C.char) *C.char {
	h := cgo.Handle(handle).Value().(snapshotSourceHandle)
	ledgerKeyB64 := C.GoString(cLedgerKey)
	var ledgerKey xdr.LedgerKey
	if err := xdr.SafeUnmarshalBase64(ledgerKeyB64, &ledgerKey); err != nil {
		panic(err)
	}
	present, entry, err := h.readTx.GetLedgerEntry(ledgerKey)
	if err != nil {
		h.logger.Errorf("SnapshotSourceGet(): GetLedgerEntry() failed: %v", err)
		return nil
	}
	if !present {
		return nil
	}
	out, err := xdr.MarshalBase64(entry)
	if err != nil {
		panic(err)
	}
	return C.CString(out)
}

// SnapshotSourceHas takes LedgerKey XDR in base64 and returns whether it exists
// It's used by the Rust preflight code to obtain ledger entries.
//
//export SnapshotSourceHas
func SnapshotSourceHas(handle C.uintptr_t, cLedgerKey *C.char) C.int {
	h := cgo.Handle(handle).Value().(snapshotSourceHandle)
	ledgerKeyB64 := C.GoString(cLedgerKey)
	var ledgerKey xdr.LedgerKey
	if err := xdr.SafeUnmarshalBase64(ledgerKeyB64, &ledgerKey); err != nil {
		panic(err)
	}
	present, _, err := h.readTx.GetLedgerEntry(ledgerKey)
	if err != nil {
		h.logger.Errorf("SnapshotSourceHas(): GetLedgerEntry() failed: %v", err)
		return 0
	}
	if present {
		return 1
	}
	return 0
}

//export FreeGoCString
func FreeGoCString(str *C.char) {
	C.free(unsafe.Pointer(str))
}

type SimulateTransactionRequest struct {
	Transaction string `json:"transaction"`
}

type SimulateTransactionCost struct {
	CPUInstructions uint64 `json:"cpuInsns,string"`
	MemoryBytes     uint64 `json:"memBytes,string"`
}

type SimulateTransactionResult struct {
	Auth      []string `json:"auth"`
	Footprint string   `json:"footprint"`
	XDR       string   `json:"xdr"`
}

type SimulateTransactionResponse struct {
	Error        string                      `json:"error,omitempty"`
	Results      []SimulateTransactionResult `json:"results,omitempty"`
	Cost         SimulateTransactionCost     `json:"cost"`
	LatestLedger int64                       `json:"latestLedger,string"`
}

// NewSimulateTransactionHandler returns a json rpc handler to run preflight simulations
func NewSimulateTransactionHandler(logger *log.Entry, networkPassphrase string, ledgerEntryReader db.LedgerEntryReader) jrpc2.Handler {
	return handler.New(func(ctx context.Context, request SimulateTransactionRequest) SimulateTransactionResponse {
		var txEnvelope xdr.TransactionEnvelope
		if err := xdr.SafeUnmarshalBase64(request.Transaction, &txEnvelope); err != nil {
			logger.WithError(err).WithField("request", request).
				Info("could not unmarshal simulate transaction envelope")
			return SimulateTransactionResponse{
				Error: "Could not unmarshal transaction",
			}
		}
		if len(txEnvelope.Operations()) != 1 {
			return SimulateTransactionResponse{
				Error: "Transaction contains more than one operation",
			}
		}
		op := txEnvelope.Operations()[0]

		var sourceAccount xdr.AccountId
		if opSourceAccount := op.SourceAccount; opSourceAccount != nil {
			sourceAccount = opSourceAccount.ToAccountId()
		} else {
			// FIXME: SourceAccount() panics, so, the user can doctor an envelope which makes the server crash
			sourceAccount = txEnvelope.SourceAccount().ToAccountId()
		}

		xdrOp, ok := op.Body.GetInvokeHostFunctionOp()
		if !ok {
			return SimulateTransactionResponse{
				Error: "Transaction does not contain invoke host function operation",
			}
		}

		hfB64, err := xdr.MarshalBase64(xdrOp.Function)
		if err != nil {
			return SimulateTransactionResponse{
				Error: "Cannot marshal host function",
			}
		}
		hfCString := C.CString(hfB64)
		sourceAccountB64, err := xdr.MarshalBase64(sourceAccount)
		if err != nil {
			return SimulateTransactionResponse{
				Error: "Cannot marshal source account",
			}
		}
		readTx, err := ledgerEntryReader.NewTx(ctx)
		if err != nil {
			return SimulateTransactionResponse{
				Error: "Cannot create db transaction",
			}
		}
		defer func() {
			_ = readTx.Done()
		}()
		latestLedger, err := readTx.GetLatestLedgerSequence()
		if err != nil {
			return SimulateTransactionResponse{
				Error: "Cannot read latest ledger",
			}
		}
		li := C.CLedgerInfo{
			network_passphrase: C.CString(networkPassphrase),
			sequence_number:    C.uint(latestLedger),
			protocol_version:   20,
			timestamp:          C.uint64_t(time.Now().Unix()),
			// Current base reserve is 0.5XLM (in stroops)
			base_reserve: 5_000_000,
		}

		sourceAccountCString := C.CString(sourceAccountB64)
		handle := cgo.NewHandle(snapshotSourceHandle{readTx, logger})
		defer handle.Delete()
		res := C.preflight_host_function(
			C.uintptr_t(handle),
			hfCString,
			sourceAccountCString,
			li,
		)
		C.free(unsafe.Pointer(hfCString))
		C.free(unsafe.Pointer(sourceAccountCString))
		defer C.free_preflight_result(res)

		if res.error != nil {
			return SimulateTransactionResponse{
				Error:        C.GoString(res.error),
				LatestLedger: int64(latestLedger),
			}
		}

		// Get the auth data
		var auth []string
		if res.auth != nil {

			// CGo doesn't have an easy way to do pointer arithmetic so,
			// we are better off transforming the memory buffer into a large slice
			// and finding the NULL termination after that
			for _, a := range unsafe.Slice(res.auth, 1<<20) {
				if a == nil {
					// we found the ending nil
					break
				}
				auth = append(auth, C.GoString(a))
			}
		}

		return SimulateTransactionResponse{
			Results: []SimulateTransactionResult{
				{
					Auth:      auth,
					Footprint: C.GoString(res.preflight),
					XDR:       C.GoString(res.result),
				},
			},
			Cost: SimulateTransactionCost{
				CPUInstructions: uint64(res.cpu_instructions),
				MemoryBytes:     uint64(res.memory_bytes),
			},
			LatestLedger: int64(latestLedger),
		}
	})
}
