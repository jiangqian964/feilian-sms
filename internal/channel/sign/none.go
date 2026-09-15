package sign

// noneSigner 不做签名，恒返回空串。
type noneSigner struct{}

func (noneSigner) Name() string { return StrategyNone }

func (noneSigner) Sign(_, _ string) (string, error) {
	return "", nil
}
