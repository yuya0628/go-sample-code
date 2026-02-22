/*
*************************
before
仕様 → キャンセルルール、割引、支払い条件
変換 → API入力JSON→内部モデル、内部→レスポンス
副作用 → DB、外部決済、Queue publish
が一箇所にいる。
*************************
*/

type CheckoutService struct {
    db        *sql.DB
    payment   PaymentGatewayClient
    publisher EventPublisher
}

type CheckoutRequest struct {
    UserID   string `json:"user_id"`
    OrderID  string `json:"order_id"`
    Coupon   string `json:"coupon"`
}

type CheckoutResponse struct {
    OrderID string `json:"order_id"`
    Status  string `json:"status"`
}

func (s *CheckoutService) Checkout(ctx context.Context, req CheckoutRequest) (CheckoutResponse, error) {
    // 変換・正規化（DTOっぽい責務）
    coupon := strings.TrimSpace(strings.ToUpper(req.Coupon))

    // 副作用（DB）
    order, err := loadOrderFromDB(ctx, s.db, req.OrderID)
    if err != nil { return CheckoutResponse{}, err }

    // 判断（ビジネスルール）
    if order.Status != "pending" {
        return CheckoutResponse{}, errors.New("invalid status")
    }
    if coupon != "" {
        order.Amount = applyDiscount(order.Amount, coupon) // 仕様変更が起きやすい
    }

    // 副作用（外部API）
    if err := s.payment.Charge(ctx, req.UserID, order.Amount); err != nil {
        return CheckoutResponse{}, err
    }

    // 副作用（DB）
    if err := updateOrderStatus(ctx, s.db, order.ID, "paid"); err != nil {
        return CheckoutResponse{}, err
    }

    // 副作用（Queue）
    _ = s.publisher.Publish(ctx, "order.paid", map[string]any{"order_id": order.ID})

    // 変換（レスポンス生成）
    return CheckoutResponse{OrderID: order.ID, Status: "paid"}, nil
}

/*
このクラスを変更する理由は複数あります。例：

・仕様変更：クーポン適用ルール、ステータス遷移、支払い条件
・DB変更：ordersテーブルのカラム名や取得方法変更
・外部API変更：決済APIのリクエスト/レスポンス変更、認証方式変更
・イベント仕様：publishするトピック名やpayload変更
・UI/API仕様：CheckoutRequest/Responseの変更

SRP違反は「このクラスは色んな都合の境界線が全部入ってる」状態です。境界が多い＝壊れやすい。

アクションにある「変更理由ごとに分割する。基本は 判断／変換／副作用 を分離」は、ここを具体的に切る指針です。
*/


/*
*************************
after
こう分けると、変更理由が分離されます。

・DB変更 → OrderRepository 実装だけ触ればよい
・決済API変更 → PaymentGateway 実装だけ触ればよい
・API入出力変更 → DTO/Mapper だけ触ればよい
・ビジネスルール変更 → Order / Usecase の判断部分だけ触ればよい

これがSRPの実利です。「修正の波及範囲を狭める」。
*************************
*/

// 1. 判断（ドメイン/ユースケース）を分離
// ・I/Oなし
// ・入力→出力（または状態遷移）に寄せる
// 例：注文の意思決定を Order / Domainに置く
type OrderStatus string
const (
    Pending OrderStatus = "pending"
    Paid    OrderStatus = "paid"
)

type Order struct {
    ID     string
    Status OrderStatus
    Amount int64
}

func (o Order) CanCheckout() bool {
    return o.Status == Pending
}

// 2. 変換（DTO/Mapper）を分離
// ・HTTP JSONなどの表現をここで吸収する
// ・内部ドメインを外部仕様から守る
type CheckoutDTO struct {
    UserID  string `json:"user_id"`
    OrderID string `json:"order_id"`
    Coupon  string `json:"coupon"`
}

func (d CheckoutDTO) ToCommand() CheckoutCommand {
    return CheckoutCommand{
        UserID:  d.UserID,
        OrderID: d.OrderID,
        Coupon:  strings.TrimSpace(strings.ToUpper(d.Coupon)),
    }
}

// 3. 副作用（DB/HTTP/Queue）を分離
// ・Repository：DB
// ・Gateway：外部API
// ・Publisher：イベント
type OrderRepository interface {
    Find(ctx context.Context, id string) (Order, error)
    UpdateStatus(ctx context.Context, id string, status OrderStatus) error
}

type PaymentGateway interface {
    Charge(ctx context.Context, userID string, amount int64) error
}

type EventPublisher interface {
    Publish(ctx context.Context, topic string, payload any) error
}

// 4. そして Usecase は“手順”だけを持つ
type CheckoutUsecase struct {
    orderRepo OrderRepository
    payment   PaymentGateway
    publisher EventPublisher
}

type CheckoutCommand struct {
    UserID  string
    OrderID string
    Coupon  string
}

func (uc *CheckoutUsecase) Checkout(ctx context.Context, cmd CheckoutCommand) error {
    // DBから注文を取得
    order, err := uc.orderRepo.Find(ctx, cmd.OrderID)
    if err != nil { return err }

    // 判断（ドメインロジック）
    if !order.CanCheckout() {
        return errors.New("invalid status")
    }

    amount := order.Amount
    if cmd.Coupon != "" {
        amount = applyDiscount(amount, cmd.Coupon) // 仕様変更が起きやすいのでドメインへ寄せる
    }

    // 外部APIで課金
    if err := uc.payment.Charge(ctx, cmd.UserID, amount); err != nil {
        return err
    }

    // DBのステータス更新
    if err := uc.orderRepo.UpdateStatus(ctx, order.ID, Paid); err != nil {
        return err
    }

    // イベント発行
    return uc.publisher.Publish(ctx, "order.paid", map[string]any{"order_id": order.ID})
}