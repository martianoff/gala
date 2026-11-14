package inout_go_wrapper

// Go calls the exported inout declarations of ledger.gala as Go functions
// and methods taking pointers.

// GoTransferToSelf transfers between an account and itself, which only Go
// can do. Each argument is a copy, copied back from left to right, so the
// result is the balance after the deposit to the second copy.
func GoTransferToSelf(balance int) int {
	a := &Account{Balance: balance}
	Transfer(a, a, 5)
	return a.Balance
}

// GoDepositRecovered deposits a negative amount, recovers the panic, and
// returns the balance: the copy back is skipped, so it is unchanged.
func GoDepositRecovered(balance int) (after int) {
	a := &Account{Balance: balance}
	defer func() {
		_ = recover()
		after = a.Balance
	}()
	a.Deposit(-1)
	return a.Balance
}

// GoDeposit deposits through the Go method.
func GoDeposit(balance, amount int) int {
	a := &Account{Balance: balance}
	a.Deposit(amount)
	return a.Balance
}
