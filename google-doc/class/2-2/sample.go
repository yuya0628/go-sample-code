/*
*************************
before
・モックが多い／セットアップが長い
→ 1つのユースケースが DB、外部API、Queue、Clock、Logger…に同時に触っている（責務が混ざっている）
・テストが遅い
→ I/O が混じっている、もしくは本物の依存を叩いている
・ケース追加が苦痛／壊れやすい
→ テストしたいのは「ルール」なのに、周辺の I/O や実装詳細にテストが引きずられている（過剰モック / 実装結合）
*************************
*/
type CheckoutUsecase struct {
    orderRepo   OrderRepo
    userRepo    UserRepo
    payment     PaymentGateway
    publisher   EventPublisher
    clock       Clock
}

func (uc *CheckoutUsecase) Checkout(ctx context.Context, orderID string) error {
    order, _ := uc.orderRepo.Find(orderID)
    user, _ := uc.userRepo.Find(order.UserID)

    if order.Status != "pending" {
        return errors.New("invalid")
    }
    if uc.clock.Now().After(order.ExpireAt) {
        return errors.New("expired")
    }

    if err := uc.payment.Charge(user.CardToken, order.Amount); err != nil {
        return err
    }

    order.Status = "paid"
    _ = uc.orderRepo.Save(order)
    _ = uc.publisher.Publish("order.paid", order.ID)
    return nil
}

/*
問題点： この単体テストを書くと、最低でも

・OrderRepo モック
・UserRepo モック
・PaymentGateway モック
・EventPublisher モック
・Clock モック

が必要になり、セットアップが長くなり、ケース追加のたびにモックの期待値をいじる羽目になります。壊れやすいのは「Checkout のルール」を見たいだけなのに、「Save が何回呼ばれたか」「Publish の引数が何か」みたいな実装詳細にテストが寄ってしまうからです。

なので、
「依存を減らす」のではなく “種類を分ける”ようにする

つまり、テスト対象を2種類に分離します。

A. 純粋ロジック（入力→出力が決まる）＝速い単体テスト
B. I/O の配線・連携（DB/HTTP/Queue）＝少数の結合テスト
*/


/*
*************************
after
*************************
*/

// CheckoutDecision はチェックアウトの結果を表す構造体
type CheckoutDecision struct {
    NextStatus OrderStatus
    NeedCharge bool
}

// DecideCheckout は注文の状態と現在時刻に基づいて、次のステータスと課金の必要性を判断する関数
func (o Order) DecideCheckout(now time.Time) (CheckoutDecision, error) {
    if o.Status != StatusPending {
        return CheckoutDecision{}, errors.New("invalid status")
    }
    if now.After(o.ExpireAt) {
        return CheckoutDecision{}, errors.New("expired")
    }

    return CheckoutDecision{
        NextStatus: StatusPaid,
        NeedCharge: true,
    }, nil
}

type CheckoutUsecase struct {
    orderRepo OrderRepository
    payment   PaymentGateway
    publisher EventPublisher
    clock     Clock
}

func (uc *CheckoutUsecase) Checkout(ctx context.Context, orderID string) error {
    order, err := uc.orderRepo.Find(ctx, orderID)
    if err != nil {
        return err
    }

    decision, err := order.DecideCheckout(uc.clock.Now())
    if err != nil {
        return err
    }

    if decision.NeedCharge {
        if err := uc.payment.Charge(ctx, order.CardToken, order.Amount); err != nil {
            return err
        }
    }

    if err := uc.orderRepo.UpdateStatus(ctx, order.ID, decision.NextStatus); err != nil {
        return err
    }

    if err := uc.publisher.Publish(ctx, "order.paid", map[string]any{
        "order_id": order.ID,
    }); err != nil {
        return err
    }

    return nil
}