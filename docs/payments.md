# Payments (Stripe Checkout)

## Flow (text diagram)
1) Client requests `/stripe/checkout` with project, freelancer, amount, currency.
2) Backend validates ownership and creates a payment record.
3) Backend creates Stripe Checkout Session and returns `checkout_url`.
4) Client is redirected to Stripe Checkout and completes payment.
5) Stripe calls `/stripe/webhook` (source of truth for payment status).
6) Backend verifies signature and updates payment status.

## Frontend API Contract
See `docs/frontend-api.md` for `/stripe/checkout`.

## Stripe Test Cards
- Success: `4242 4242 4242 4242`
- Declined: `4000 0000 0000 0002`
- Requires authentication: `4000 0025 0000 3155`

Use any future date, any CVC, and any ZIP.

