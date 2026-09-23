package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	appv7 "github.com/larksuite/oapi-sdk-go/v3/service/application/v7"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	qrcode "github.com/skip2/go-qrcode"
)

// Credentials never enter this browser-facing state. Each attempt belongs to
// the authenticated web session that initiated it and is kept only in memory.
type FeishuSetup struct {
	ID           string `json:"id"`
	Phase        string `json:"phase"`
	Message      string `json:"message"`
	QR           string `json:"qr,omitempty"`
	URL          string `json:"url,omitempty"`
	Expires      int64  `json:"expires"`
	TaskID       string `json:"task_id,omitempty"`
	Menu         string `json:"menu"`
	Connection   string `json:"connection"`
	ownerSession string
	cancel       context.CancelFunc
}

func (f *Feishu) setupBusy() bool {
	return f.setup != nil && (f.setup.Phase == "starting" || f.setup.Phase == "scanning" || f.setup.Phase == "configuring")
}

func (f *Feishu) beginSetup(session, task string) (FeishuSetup, error) {
	f.setupMu.Lock()
	defer f.setupMu.Unlock()
	if f.setupBusy() {
		return FeishuSetup{}, errors.New("已有扫码创建正在进行，请等待完成或取消")
	}
	cfg := f.app.config.get().Feishu
	if cfg.AppID != "" || cfg.Secret != "" || cfg.Owner != "" {
		return FeishuSetup{}, errors.New("已配置飞书应用，不会覆盖已有机器人；请使用现有应用配对入口")
	}
	if task != "" {
		if _, err := f.app.store.task(task); err != nil {
			return FeishuSetup{}, errors.New("任务不存在")
		}
	}
	ctx, cancel := context.WithTimeout(f.app.ctx, 10*time.Minute)
	state := FeishuSetup{ID: uid(), Phase: "starting", Message: "正在向飞书申请二维码…", Expires: time.Now().Add(10 * time.Minute).UnixMilli(), TaskID: task, Menu: "等待创建", Connection: "未连接", ownerSession: session, cancel: cancel}
	f.setup = &state
	f.app.wg.Add(1)
	go func() { defer f.app.wg.Done(); defer cancel(); f.runSetup(ctx, state.ID) }()
	return state, nil
}

func (f *Feishu) setupState(session, id string) (FeishuSetup, error) {
	f.setupMu.Lock()
	defer f.setupMu.Unlock()
	if f.setup == nil || f.setup.ID != id || f.setup.ownerSession != session {
		return FeishuSetup{}, errors.New("扫码会话不存在或不属于当前登录")
	}
	s := *f.setup
	s.Connection = f.status()
	return s, nil
}
func (f *Feishu) cancelSetup(session, id string) error {
	f.setupMu.Lock()
	defer f.setupMu.Unlock()
	if f.setup == nil || f.setup.ID != id || f.setup.ownerSession != session {
		return errors.New("扫码会话不存在")
	}
	if f.setup.Phase == "configuring" {
		return errors.New("飞书已返回应用，正在保存和连接，请等待完成")
	}
	if f.setup.Phase == "starting" || f.setup.Phase == "scanning" {
		f.setup.cancel()
		f.setup.Phase = "cancelled"
		f.setup.Message = "已取消等待；若已在飞书确认创建，请到飞书后台检查该应用"
		f.setup.QR = ""
		f.setup.URL = ""
	}
	return nil
}
func (f *Feishu) setupUpdate(id string, update func(*FeishuSetup)) {
	f.setupMu.Lock()
	defer f.setupMu.Unlock()
	if f.setup != nil && f.setup.ID == id && f.setup.Phase != "cancelled" {
		update(f.setup)
	}
}

func (f *Feishu) runSetup(ctx context.Context, id string) {
	preset := false
	opts := &registration.Options{CreateOnly: true, AppPreset: &registration.AppPreset{Name: "Duo助手", Desc: "本地 Codex 任务与知识工作台"}, Addons: &registration.AppAddons{Preset: &preset,
		Callbacks: registration.AppAddonsCallbacks{Items: []string{"card.action.trigger"}},
		Scopes:    registration.AppAddonsScopes{Tenant: []string{"im:message.p2p_msg:readonly", "im:message:send_as_bot", "application:application:patch"}},
		Events:    registration.AppAddonsEvents{Items: registration.AppAddonsEventItems{Tenant: []string{"im.message.receive_v1", "application.bot.menu_v6"}}}}}
	opts.OnQRCode = func(info *registration.QRCodeInfo) {
		u, err := url.Parse(info.URL)
		if err != nil || u.Scheme != "https" || (u.Host != "accounts.feishu.cn" && u.Host != "accounts.larksuite.com" && u.Host != "open.feishu.cn" && u.Host != "open.larksuite.com") {
			return
		}
		png, err := qrcode.Encode(info.URL, qrcode.Medium, 320)
		if err != nil {
			return
		}
		f.setupUpdate(id, func(s *FeishuSetup) {
			s.Phase = "scanning"
			s.Message = "请用飞书扫码，在官方页面确认创建与授权"
			s.URL = info.URL
			s.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			s.Expires = time.Now().Add(time.Duration(min(info.ExpireIn, 600)) * time.Second).UnixMilli()
		})
	}
	result, err := f.register(ctx, opts)
	if err != nil {
		message := "飞书创建未完成，请重试；也可能受企业审批策略限制"
		var denied *registration.AccessDeniedError
		var expired *registration.ExpiredError
		if errors.As(err, &denied) {
			message = "已拒绝飞书授权"
		}
		if errors.As(err, &expired) || errors.Is(err, context.DeadlineExceeded) {
			message = "二维码已过期，请重新生成"
		}
		f.setupUpdate(id, func(s *FeishuSetup) { s.Phase = "failed"; s.Message = message; s.QR = ""; s.URL = "" })
		return
	}
	// Serialize credential commit with settings and cancellation. Never replace
	// an existing application or bind an identity supplied by the browser.
	f.setupMu.Lock()
	if f.setup == nil || f.setup.ID != id || f.setup.Phase == "cancelled" {
		f.setupMu.Unlock()
		return
	}
	f.setup.QR = ""
	f.setup.URL = ""
	if ctx.Err() != nil {
		f.setup.Phase = "failed"
		f.setup.Message = "扫码等待已超时，请在飞书后台检查是否已创建应用"
		f.setupMu.Unlock()
		return
	}
	if result == nil || result.ClientID == "" || result.ClientSecret == "" || result.UserInfo == nil || result.UserInfo.OpenID == "" || result.UserInfo.TenantBrand == "lark" {
		f.setup.Phase = "failed"
		f.setup.Message = "注册结果缺少身份或不是飞书中国版，请在飞书后台检查创建结果"
		f.setupMu.Unlock()
		return
	}
	cfg := f.app.config.get()
	if cfg.Feishu.AppID != "" || cfg.Feishu.Secret != "" || cfg.Feishu.Owner != "" {
		f.setup.Phase = "failed"
		f.setup.Message = "配置已变更，未覆盖原有应用"
		f.setupMu.Unlock()
		return
	}
	cfg.Feishu = FeishuConfig{Enabled: true, AppID: result.ClientID, Secret: result.ClientSecret, Owner: result.UserInfo.OpenID}
	if err = f.app.config.save(cfg); err != nil {
		f.setup.Phase = "failed"
		f.setup.Message = "应用已创建但本机保存失败，请保留飞书后台的应用信息"
		f.setupMu.Unlock()
		return
	}
	task := f.setup.TaskID
	f.setup.Phase = "configuring"
	f.setup.Message = "账号已绑定，正在初始化菜单和连接机器人"
	f.setup.Menu = "正在初始化"
	f.setupMu.Unlock()
	// Once credentials are committed, finish setup even if QR polling expired.
	finish, stop := context.WithTimeout(f.app.ctx, 90*time.Second)
	defer stop()
	menuErr := f.initializeMenu(finish, cfg.Feishu)
	f.setupUpdate(id, func(s *FeishuSetup) {
		if menuErr != nil {
			s.Menu = "菜单初始化或发布未完成，请到飞书后台检查审批；不会自动重复发布"
		} else {
			s.Menu = "菜单已提交发布（是否生效取决于飞书审批）"
		}
	})
	f.startRegistered()
	chat, chatErr := f.welcome(finish, cfg.Feishu, id)
	if chatErr == nil && chat != "" {
		chatErr = f.app.store.set("feishu_chat", chat)
		if chatErr == nil && task != "" {
			chatErr = f.app.bind(chat, task)
		}
	} else if chatErr == nil {
		chatErr = errors.New("missing chat")
	}
	f.setupUpdate(id, func(s *FeishuSetup) {
		s.Phase = "complete"
		s.Message = "账号绑定成功，已发送绑定通知。长连接状态见下方"
		if task != "" && chatErr == nil {
			s.Message = "账号和当前任务绑定成功，已发送绑定通知。长连接状态见下方"
		}
		if chatErr != nil || menuErr != nil {
			s.Phase = "partial"
		}
		if chatErr != nil {
			s.Message = "账号凭据已保存，但私聊或任务绑定未完成。请给机器人发送一条消息，再在网页连接任务"
		}
	})
}

func initializeFeishuMenu(ctx context.Context, c FeishuConfig) error {
	client := lark.NewClient(c.AppID, c.Secret)
	cfg, err := client.Application.V7.ApplicationConfig.Patch(ctx, appv7.NewPatchApplicationConfigReqBuilder().AppId(c.AppID).Body(appv7.NewPatchApplicationConfigReqBodyBuilder().Callback(appv7.NewAppConfigCallbackBuilder().CallbackType("websocket").AddCallbacks([]string{"card.action.trigger"}).Build()).Event(appv7.NewAppConfigEventBuilder().SubscriptionType("websocket").AddEvents([]string{"im.message.receive_v1", "application.bot.menu_v6"}).Build()).Build()).Build())
	if err != nil {
		return err
	}
	if !cfg.Success() {
		return fmt.Errorf("config rejected: %d", cfg.Code)
	}
	menus := []*appv7.BotMenuNode{}
	for i, item := range []struct{ key, name string }{{"tasks", "我的任务"}, {"knowledge", "整理知识"}} {
		menus = append(menus, appv7.NewBotMenuNodeBuilder().MenuId("jianzuo_"+item.key).Sort(i+1).DefaultName(item.name).I18nName(map[string]string{"zh_cn": item.name}).MenuContentType(2).EventKey("jianzuo."+item.key).Build())
	}
	ability, err := client.Application.V7.ApplicationAbility.Patch(ctx, appv7.NewPatchApplicationAbilityReqBuilder().AppId(c.AppID).Body(appv7.NewPatchApplicationAbilityReqBodyBuilder().Bot(appv7.NewAppAbilityBotBuilder().Enable(true).BotMenuEnable(true).BotMenus(menus).BotMenuDisplayStrategy(1).Build()).Build()).Build())
	if err != nil {
		return err
	}
	if !ability.Success() {
		return fmt.Errorf("menu rejected: %d", ability.Code)
	}
	published, err := client.Application.V7.ApplicationPublish.Create(ctx, appv7.NewCreateApplicationPublishReqBuilder().AppId(c.AppID).Body(appv7.NewCreateApplicationPublishReqBodyBuilder().MobileDefaultAbility("bot").PcDefaultAbility("bot").Remark("Duo首次初始化").Changelog("启用私聊任务与机器人菜单").Build()).Build())
	if err != nil {
		return err
	}
	if !published.Success() {
		return fmt.Errorf("publish rejected: %d", published.Code)
	}
	return nil
}
func welcomeFeishu(ctx context.Context, c FeishuConfig, id string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": "已绑定Duo。发送 /任务 查看任务，或使用下方菜单。网页绑定当前任务后可直接发消息继续。"})
	r, err := lark.NewClient(c.AppID, c.Secret).Im.Message.Create(ctx, im.NewCreateMessageReqBuilder().ReceiveIdType("open_id").Body(im.NewCreateMessageReqBodyBuilder().ReceiveId(c.Owner).MsgType("text").Content(string(content)).Uuid(id).Build()).Build())
	if err != nil {
		return "", err
	}
	if !r.Success() || r.Data == nil {
		return "", errors.New("welcome failed")
	}
	return value(r.Data.ChatId), nil
}

func (s *Server) setupRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/feishu/setup", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			TaskID string `json:"task_id"`
		}
		if !body(w, r, &v) {
			return
		}
		session, _ := s.identity(r)
		state, err := s.app.feishu.beginSetup(session, v.TaskID)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		jsonOut(w, 202, state)
	}))
	m.HandleFunc("GET /api/feishu/setup/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		session, _ := s.identity(r)
		state, err := s.app.feishu.setupState(session, r.PathValue("id"))
		if err != nil {
			fail(w, 404, err.Error())
			return
		}
		jsonOut(w, 200, state)
	}))
	m.HandleFunc("POST /api/feishu/setup/{id}/cancel", s.secure(func(w http.ResponseWriter, r *http.Request) {
		session, _ := s.identity(r)
		if err := s.app.feishu.cancelSetup(session, r.PathValue("id")); err != nil {
			fail(w, 409, err.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
}
