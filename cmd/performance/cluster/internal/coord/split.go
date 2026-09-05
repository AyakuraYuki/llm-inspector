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
