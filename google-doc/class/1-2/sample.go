/*
*************************
before
依存逆転違反
CheckoutUsecase（コア）が Stripe SDK に直接依存している。

外部API仕様が変わると：
・CheckoutUsecase修正必要
・ドメイン影響
・テスト壊れる
コアが不安定になります
*************************
*/
type CheckoutUsecase struct {
    stripe *stripe.Client
}

func (uc *CheckoutUsecase) Checkout(order Order) error {
    _, err := uc.stripe.Charges.New(&stripe.ChargeParams{
        Amount: order.Amount,
    })
    return err
}

/*
*************************
after
正しい構造（Port & Adapter）
*************************
*/

// Port（コア側）：ドメインが依存するインターフェース
type PaymentGateway interface {
    Charge(amount int64) error
}

// ここでは Stripe を知らない
type CheckoutUsecase struct {
    payment PaymentGateway
}
// ここでは Stripe を知らない
func (uc *CheckoutUsecase) Checkout(order Order) error {
    return uc.payment.Charge(order.Amount)
}

// Adapter（外側）：外部APIを Port に適合させる
// PaymentGateway の具象クラス
type StripePaymentGateway struct {
    client *stripe.Client
}

func (g *StripePaymentGateway) Charge(amount int64) error {
    _, err := g.client.Charges.New(&stripe.ChargeParams{
        Amount: amount,
    })
    return err
}