package coord

// Split 把全局并发数切分成 n 份：基数为 global/n，余数逐台 +1（前 r 台）。
// 份额为 0 的 agent 跳过该档位。切分按 agent 下标单调：对同一下标 i，
// c1 <= c2 时 Split(c1, n)[i] <= Split(c2, n)[i]，因此
// Split(max(concurrency), n)[i] 是 agent i 在整个 run 中的并发上界，
// session 建立时按它一次性配置连接池即可。
func Split(global, n int) []int {
	shares := make([]int, n)
	if n <= 0 || global <= 0 {
		return shares
	}
	base, r := global/n, global%n
	for i := range shares {
		shares[i] = base
		if i < r {
			shares[i]++
		}
	}
	return shares
}

// SplitAmongActive 把全局请求数 total 只分给并发份额非零的 agent（shares[i] > 0），
// 返回与 shares 等长的切片，份额为零的 agent 对应 0。请求数制下 agent 是否参与
// 由并发切分决定：一个没拿到 worker 的 agent 即便分到请求配额也无人发送，
// 那部分配额会凭空消失，导致全局实际发出的请求数少于配置值。
func SplitAmongActive(total int, shares []int) []int {
	limits := make([]int, len(shares))
	if total <= 0 {
		return limits
	}
	active := 0
	for _, s := range shares {
		if s > 0 {
			active++
		}
	}
	if active == 0 {
		return limits
	}
	parts := Split(total, active)
	j := 0
	for i, s := range shares {
		if s > 0 {
			limits[i] = parts[j]
			j++
		}
	}
	return limits
}
