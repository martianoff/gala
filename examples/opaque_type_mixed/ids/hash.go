package ids

// Hash is declared here, by hand, so the transpiler must not generate
// AccountID's Hash. Accounts hash by their number modulo 1000.
func (a AccountID) Hash() uint32 {
	return uint32(int64(a) % 1000)
}
