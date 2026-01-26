package g

var globalLogicGoroutine *Go

func SetGlobalLogic(l *Go) {
	globalLogicGoroutine = l
}

func GlobalLogic() *Go {
	return globalLogicGoroutine
}
