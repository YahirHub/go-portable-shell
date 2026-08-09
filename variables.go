package portablesh

import "fmt"

func assignVariable(state *shellState, name, value string) error {
	if state.readonly[name] {
		return fmt.Errorf("%s: readonly variable", name)
	}
	state.env[name] = value
	return nil
}

func unsetVariable(state *shellState, name string) error {
	if state.readonly[name] {
		return fmt.Errorf("%s: readonly variable", name)
	}
	delete(state.env, name)
	delete(state.exported, name)
	return nil
}

func rememberLocal(state *shellState, name string) error {
	if len(state.locals) == 0 {
		return fmt.Errorf("local: can only be used in a function")
	}
	frame := state.locals[len(state.locals)-1]
	if _, exists := frame[name]; exists {
		return nil
	}
	value, set := state.env[name]
	frame[name] = localValue{value: value, set: set, exported: state.exported[name], readonly: state.readonly[name]}
	return nil
}

func restoreLocalFrame(state *shellState) {
	if len(state.locals) == 0 {
		return
	}
	frame := state.locals[len(state.locals)-1]
	state.locals = state.locals[:len(state.locals)-1]
	for name, previous := range frame {
		if previous.set {
			state.env[name] = previous.value
		} else {
			delete(state.env, name)
		}
		if previous.exported {
			state.exported[name] = true
		} else {
			delete(state.exported, name)
		}
		if previous.readonly {
			state.readonly[name] = true
		} else {
			delete(state.readonly, name)
		}
	}
}
