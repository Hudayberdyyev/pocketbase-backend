package main

type stripeCheckoutRequest struct {
	ProjectID    string `json:"project_id"`
	FreelancerID string `json:"freelancer_id"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
}

type stripeConfig struct {
	SecretKey          string
	PlatformFeePercent float64
	SuccessURL         string
	CancelURL          string
}
