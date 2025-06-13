package app

import (
	"log"
	"os"
	"os/signal"
	"reflect"
	"sync/atomic"
	"syscall"
)

// 节点全局状态
const (
	AppStateNone = iota // 未开始或已停止
	AppStateInit        // 正在初始化中
	AppStateRun         // 正在运行中
	AppStateStop        // 正在停止中
)

// 单例
var defaultApp = new(App)

type Module interface {
	OnInit() error // 初始化
	OnDestroy()    // 销毁
	Run()          // 启动
	Name() string  // 名字
}

// mod 模块
type mod struct {
	mi Module
}

// DefaultApp 默认单例
func DefaultApp() *App {
	return defaultApp
}

// App 中的 modules 在初始化(通过 Start 或 Run) 之后不能变更
// App API 只有 Get 和 Stats 是 goroutine safe 的
type App struct {
	mods  []*mod
	state int32
}

// SetState 设置状态
func (app *App) setState(s int32) {
	atomic.StoreInt32(&app.state, s)
}

// GetState 获取状态
func (app *App) GetState() int32 {
	return atomic.LoadInt32(&app.state)
}

// Start 初始化app
func (app *App) start(mods ...Module) {
	// 单个app不能启动两次
	if app.GetState() != AppStateNone {
		log.Fatal("app mods cannot start twice")
	}
	if len(mods) == 0 {
		return
	}
	if len(app.mods) != 0 {
		log.Fatal("app mods cannot start twice")
	}
	log.Println("app starting up")
	// register
	for _, mi := range mods {
		m := new(mod)
		m.mi = mi
	}
	app.setState(AppStateInit)
	// 模块初始化
	for _, m := range app.mods {
		mi := m.mi
		if err := mi.OnInit(); err != nil {
			log.Fatalf("module %v init error %v", reflect.TypeOf(mi), err)
		}
	}
	// 模块启动
	for _, m := range app.mods {
		go run(m)
	}
	app.setState(AppStateRun)
	log.Println("app started")
}

func (app *App) stop() {
	if app.GetState() == AppStateStop {
		return
	}
	log.Println("app stop begin")
	app.setState(AppStateStop)
	// 先进后出
	for i := len(app.mods) - 1; i >= 0; i-- {
		m := app.mods[i]
		destroy(m)
	}
	app.setState(AppStateNone)
	log.Println("app stoped")
}

func run(m *mod) {
	m.mi.Run()
}

func destroy(m *mod) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("module destroy panic: ", r)
		}
	}()

	m.mi.OnDestroy()
}

func (app *App) Run(mods ...Module) {
	app.start(mods...)
	c := make(chan os.Signal, 1)
	for {
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		sig := <-c
		log.Printf("server closing down (signal: %v)", sig)
		if sig != syscall.SIGHUP {
			break
		}
	}

	app.stop()
}
