package ledgerstate

import (
	"fmt"
	"testing"

	"github.com/iotaledger/goshimmer/packages/objectstorage"
)

var (
	iota           = NewColor("IOTA")
	eth            = NewColor("ETH")
	transferHash1  = NewTransferHash("TRANSFER1")
	transferHash2  = NewTransferHash("TRANSFER2")
	addressHash1   = NewAddressHash("ADDRESS1")
	addressHash3   = NewAddressHash("ADDRESS3")
	addressHash4   = NewAddressHash("ADDRESS4")
	pendingReality = NewRealityId("PENDING")
)

func Test(t *testing.T) {
	ledgerState := NewLedgerState("testLedger").Prune().AddTransferOutput(
		transferHash1, addressHash1, NewColoredBalance(eth, 1337), NewColoredBalance(iota, 1338),
	)

	ledgerState.CreateReality(pendingReality)

	ledgerState.MergeRealities(pendingReality, MAIN_REALITY_ID).Release()

	ledgerState.GetReality(pendingReality).Consume(func(object objectstorage.StorableObject) {
		reality := object.(*Reality)

		transfer := NewTransfer(transferHash2).AddInput(
			NewTransferOutputReference(transferHash1, addressHash1),
		).AddOutput(
			addressHash3, NewColoredBalance(iota, 338),
		).AddOutput(
			addressHash3, NewColoredBalance(eth, 337),
		).AddOutput(
			addressHash4, NewColoredBalance(iota, 1000),
		).AddOutput(
			addressHash4, NewColoredBalance(eth, 1000),
		)

		if err := reality.BookTransfer(transfer); err != nil {
			t.Error(err)
		}

		ledgerState.ForEachTransferOutput(func(object *objectstorage.CachedObject) bool {
			object.Consume(func(object objectstorage.StorableObject) {
				fmt.Println(object.(*TransferOutput))
			})

			return true
		}, pendingReality, addressHash3, UNSPENT)

		ledgerState.ForEachTransferOutput(func(object *objectstorage.CachedObject) bool {
			object.Consume(func(object objectstorage.StorableObject) {
				fmt.Println(object.(*TransferOutput))
			})

			return true
		}, transferHash2, addressHash4)
	})
}