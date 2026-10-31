package engine

// RunSteps runs the steps of another flow inside the step that is running, as
// a part of it: for a flow that does what a command does, such as storing a
// password, and then goes on, with the recovery identity unlocked once for
// all of it.
//
// The inputs of that flow are resolved from flags, as RunWith would give
// them, and put back as they were when the steps end, so that a name both
// flows use, such as user, means one thing in each. Everything else the steps
// keep as values stays, such as the branch they worked on. The steps are not
// numbered: each is shown as a heading under the running step. Nothing is put
// back here when one fails; the error goes to the running step, and the
// undos registered so far are those of the run.
func (c *Ctx) RunSteps(inputs []Input, flags map[string]string, steps []Step) error {
	type saved struct {
		value string
		had   bool
	}
	before := map[string]saved{}
	for _, in := range inputs {
		v, had := c.Values[in.Name]
		before[in.Name] = saved{v, had}
	}
	outer := c.flags
	c.flags = flags
	defer func() {
		c.flags = outer
		for name, s := range before {
			if s.had {
				c.Values[name] = s.value
			} else {
				delete(c.Values, name)
			}
		}
	}()
	for _, in := range inputs {
		if err := resolve(c, in); err != nil {
			return err
		}
	}
	for _, s := range steps {
		if s.Hide != nil && s.Hide(c) {
			continue
		}
		title, err := c.Expand(s.Title)
		if err != nil {
			return err
		}
		why, err := c.Expand(s.Why)
		if err != nil {
			return err
		}
		c.UI.Section("·", title, why)
		if s.When != nil {
			if ok, reason := s.When(c); !ok {
				c.UI.Skip("skipped: " + reason)
				continue
			}
		}
		if err := s.Do.run(c); err != nil {
			return err
		}
	}
	return nil
}
