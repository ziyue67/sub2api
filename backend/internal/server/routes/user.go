package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterUserRoutes 注册用户相关路由（需要认证）
func RegisterUserRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	// Studio Bridge 内部接口（无需 JWT，由 handler 层校验内部 token）
	internalStudioBridge := v1.Group("/internal/studio-bridge")
	{
		internalStudioBridge.POST("/redeem", h.StudioBridge.Redeem)
		internalStudioBridge.POST("/user-summary", h.StudioBridge.UserSummary)
		internalStudioBridge.POST("/charges/reserve", h.StudioBridge.Reserve)
		internalStudioBridge.POST("/charges/commit", h.StudioBridge.Commit)
		internalStudioBridge.POST("/charges/refund", h.StudioBridge.Refund)
	}

	authenticated := v1.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(middleware.BackendModeUserGuard(settingService))
	// 面板全局按用户限流：防止单个账号高频刷接口打爆数据库
	authenticated.Use(panelRateLimiter.Global())
	// 用户管理面变更类操作入审计（含 TOTP 启用/禁用、step-up 验证、密码修改等安全事件）
	authenticated.Use(gin.HandlerFunc(auditLog))
	{
		// 用户接口
		user := authenticated.Group("/user")
		{
			user.GET("/profile", h.User.GetProfile)
			user.PUT("/password", h.User.ChangePassword)
			user.PUT("", h.User.UpdateProfile)
			user.GET("/aff", h.User.GetAffiliate)
			user.POST("/aff/transfer", h.User.TransferAffiliateQuota)
			user.POST("/account-bindings/email/send-code", h.User.SendEmailBindingCode)
			user.POST("/account-bindings/email", h.User.BindEmailIdentity)
			user.DELETE("/account-bindings/:provider", h.User.UnbindIdentity)
			user.POST("/auth-identities/bind/start", h.User.StartIdentityBinding)
			user.GET("/api-keys/:id/usage/daily", panelRateLimiter.Heavy(), h.Usage.GetMyAPIKeyDailyUsage)
			user.GET("/platform-quotas", h.User.GetMyPlatformQuotas)

			// 通知邮箱管理
			notifyEmail := user.Group("/notify-email")
			{
				notifyEmail.POST("/send-code", h.User.SendNotifyEmailCode)
				notifyEmail.POST("/verify", h.User.VerifyNotifyEmail)
				notifyEmail.PUT("/toggle", h.User.ToggleNotifyEmail)
				notifyEmail.DELETE("", h.User.RemoveNotifyEmail)
			}

			// TOTP 双因素认证
			totp := user.Group("/totp")
			{
				totp.GET("/status", h.Totp.GetStatus)
				totp.GET("/verification-method", h.Totp.GetVerificationMethod)
				totp.POST("/send-code", h.Totp.SendVerifyCode)
				totp.POST("/setup", h.Totp.InitiateSetup)
				totp.POST("/enable", h.Totp.Enable)
				totp.POST("/disable", h.Totp.Disable)
				// 敏感操作二次验证：授予当前会话一段时间的 step-up 权限
				totp.POST("/step-up", h.Totp.StepUp)
			}

			passkeys := user.Group("/passkeys")
			{
				passkeys.GET("", h.Passkey.List)
				passkeys.POST("/register/begin", h.Passkey.BeginRegistration)
				passkeys.POST("/register/finish", h.Passkey.FinishRegistration)
				passkeys.PATCH("/:id", h.Passkey.Rename)
				passkeys.DELETE("/:id", h.Passkey.Delete)
			}

			// Studio Bridge（用户侧）
			studioBridge := user.Group("/studio-bridge")
			{
				studioBridge.POST("/launch", h.StudioBridge.Launch)
				studioBridge.GET("/session-probe", h.StudioBridge.SessionProbe)
			}

			// 生图（Image Creator）
			imageCreator := user.Group("/image-creator")
			{
				imageCreator.POST("/tasks", h.ImageCreator.CreateTask)
				imageCreator.GET("/tasks", h.ImageCreator.ListTasks)
				imageCreator.GET("/tasks/:id", h.ImageCreator.GetTask)
				imageCreator.GET("/images", h.ImageCreator.ListImages)
				imageCreator.DELETE("/images", h.ImageCreator.DeleteImages)
				imageCreator.GET("/images/:id/file", h.ImageCreator.GetImageFile)
				imageCreator.GET("/images/:id/reference-file", h.ImageCreator.GetReferenceImageFile)
				imageCreator.POST("/references", h.ImageCreator.UploadReferenceImage)
			}

			// 画布（Canvas）
			canvases := user.Group("/canvases")
			{
				canvases.GET("", h.Canvas.ListCanvases)
				canvases.POST("", h.Canvas.SaveCanvas)
				canvases.GET("/:id", h.Canvas.GetCanvas)
				canvases.PUT("/:id", h.Canvas.UpdateCanvas)
				canvases.DELETE("/:id", h.Canvas.DeleteCanvas)
			}

			// 画布运行记录
			canvasRuns := user.Group("/canvas-runs")
			{
				canvasRuns.GET("", h.Canvas.ListRuns)
				canvasRuns.POST("", h.Canvas.CreateRun)
				canvasRuns.GET("/:id", h.Canvas.GetRun)
				canvasRuns.POST("/:id/cancel", h.Canvas.CancelRun)
			}

			// 画布可用模型列表
			user.GET("/canvas/models", h.Canvas.ListModels)

			// 提示词收藏
			user.GET("/prompt-favorites", h.PromptFavorite.List)
			user.POST("/prompt-favorites", h.PromptFavorite.Save)
			user.DELETE("/prompt-favorites/:id", h.PromptFavorite.Delete)
		}

		// API Key管理
		keys := authenticated.Group("/keys")
		{
			keys.GET("", h.APIKey.List)
			keys.GET("/:id", h.APIKey.GetByID)
			keys.POST("", h.APIKey.Create)
			keys.PUT("/:id", h.APIKey.Update)
			keys.DELETE("/:id", h.APIKey.Delete)
		}

		// 用户可用分组（非管理员接口）
		groups := authenticated.Group("/groups")
		{
			groups.GET("/available", h.APIKey.GetAvailableGroups)
			groups.GET("/rates", h.APIKey.GetUserGroupRates)
		}

		// 用户可用渠道（非管理员接口）
		channels := authenticated.Group("/channels")
		{
			channels.GET("/available", h.AvailableChannel.List)
		}

		// 模型广场：展示所有活跃分组中可调度账号已同步的模型。
		authenticated.GET("/model-square", h.ModelSquare.List)

		// 使用记录（聚合统计属重查询，叠加更严格的按用户限流）
		usage := authenticated.Group("/usage")
		usage.Use(panelRateLimiter.Heavy())
		{
			usage.GET("", h.Usage.List)
			usage.GET("/errors", h.Usage.ListErrors)
			usage.GET("/errors/:id", h.Usage.GetErrorDetail)
			usage.GET("/:id", h.Usage.GetByID)
			usage.GET("/stats", h.Usage.Stats)
			// User dashboard endpoints
			usage.GET("/dashboard/stats", h.Usage.DashboardStats)
			usage.GET("/dashboard/trend", h.Usage.DashboardTrend)
			usage.GET("/dashboard/models", h.Usage.DashboardModels)
			usage.GET("/dashboard/snapshot-v2", h.Usage.DashboardSnapshotV2)
			usage.POST("/dashboard/api-keys-usage", h.Usage.DashboardAPIKeysUsage)
			// Leaderboard（Token 榜：Input + Output + Cache + Image Output 求和排名，Top 1-20，普通用户脱敏）
			usage.GET("/dashboard/leaderboard", h.Usage.DashboardLeaderboard)
		}

		// 公告（用户可见）
		announcements := authenticated.Group("/announcements")
		{
			announcements.GET("", h.Announcement.List)
			announcements.POST("/:id/read", h.Announcement.MarkRead)
		}

		// 卡密兑换
		redeem := authenticated.Group("/redeem")
		{
			redeem.POST("", h.Redeem.Redeem)
			redeem.GET("/history", h.Redeem.GetHistory)
		}

		// 用户订阅
		subscriptions := authenticated.Group("/subscriptions")
		{
			subscriptions.GET("", h.Subscription.List)
			subscriptions.GET("/active", h.Subscription.GetActive)
			subscriptions.GET("/progress", h.Subscription.GetProgress)
			subscriptions.GET("/summary", h.Subscription.GetSummary)
		}

		// 渠道监控（用户只读）
		monitors := authenticated.Group("/channel-monitors")
		{
			monitors.GET("", h.ChannelMonitor.List)
			monitors.GET("/:id/status", h.ChannelMonitor.GetStatus)
		}

		// 鹈鹕测智展示（用户只读；功能关闭时返回空画廊）
		showcase := authenticated.Group("/pelican-showcase")
		{
			showcase.GET("", h.PelicanShowcase.List)
			showcase.GET("/items/:id", h.PelicanShowcase.GetItem)
		}

		// V2 passive views require feature on + mode=v2.
		monitorV2 := authenticated.Group("/channel-monitor-v2")
		monitorV2.Use(panelRateLimiter.Heavy())
		monitorV2.Use(channelMonitorModeV2Guard(settingService))
		{
			monitorV2.GET("/dimensions", h.ChannelMonitorV2.Dimensions)
			monitorV2.GET("/snapshot", h.ChannelMonitorV2.Snapshot)
			monitorV2.GET("/models", h.ChannelMonitorV2.Models)
			monitorV2.GET("/matrix", h.ChannelMonitorV2.Matrix)
			monitorV2.GET("/errors", h.ChannelMonitorV2.Errors)
			monitorV2.GET("/users", h.ChannelMonitorV2.Users)
		}
	}
}
