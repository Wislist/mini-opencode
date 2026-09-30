package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

func permTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model.(*Model)
}

// /permissions 是一个二级菜单，不是往转录里打字。
//
// 之前 `/permissions` 会把三行选项加一句说明整块 addBlock 进对话区，混在
// 正文里，还会留在历史中。改成底部覆盖层：打开时不污染转录，选完即消失。

// TestPermissionsOpensOverlayNotTranscript 断言查看选项不再写入对话区。
func TestPermissionsOpensOverlayNotTranscript(t *testing.T) {
	m := permTestModel(t)
	before := len(m.blocks)

	m = pressKey(m, key("enter")) // 先确保处于 idle
	m.handleInput("/permissions")

	if m.state != statePermissions {
		t.Fatalf("没有进入权限菜单状态: %v", m.state)
	}
	if len(m.blocks) != before {
		t.Fatalf("菜单被写进了对话区（blocks %d -> %d）", before, len(m.blocks))
	}
	// 菜单内容仍然可见，只是在覆盖层里。标签与 CLI 的 /permissions 输出保持一致
	// （中文），三档都在。
	menu := plainText(m.renderPermissionsMenu())
	for _, want := range []string{"完全信任", "替我审核", "请求批准"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("菜单缺少 %q:\n%s", want, menu)
		}
	}
}

// TestPermissionsMenuNavigatesAndApplies 断言上下选择 + enter 生效。
func TestPermissionsMenuNavigatesAndApplies(t *testing.T) {
	m := permTestModel(t)
	m.handleInput("/permissions")

	// 光标默认停在当前档（请求批准）；上移一档到 auto-review
	m = pressKey(m, key("up"))
	m = pressKey(m, key("enter"))

	if m.PermissionMode() != agent.PermissionModeAutoReview {
		t.Fatalf("选择后模式为 %q，want auto-review", m.PermissionMode())
	}
	if m.state != stateIdle {
		t.Fatalf("选择后没有回到 idle: %v", m.state)
	}
}

// TestPermissionsMenuEscCancels 断言 esc 不改变模式。
func TestPermissionsMenuEscCancels(t *testing.T) {
	m := permTestModel(t)
	original := m.PermissionMode()
	m.handleInput("/permissions")
	m = pressKey(m, key("down"))
	m = pressKey(m, key("esc"))

	if m.PermissionMode() != original {
		t.Fatalf("esc 之后模式被改成 %q", m.PermissionMode())
	}
	if m.state != stateIdle {
		t.Fatalf("esc 之后没有回到 idle: %v", m.state)
	}
}

// TestPermissionsFullAccessNeedsConfirmation 断言完全信任仍需二次确认 ——
// 菜单化不能把这道闸门去掉。
func TestPermissionsFullAccessNeedsConfirmation(t *testing.T) {
	m := permTestModel(t)
	m.handleInput("/permissions")

	// 菜单按信任度从高到低排列，默认光标停在当前档（请求批准）；
	// 移到 full-access 并选择。
	m = pressKey(m, key("up"))
	m = pressKey(m, key("up"))
	m = pressKey(m, key("enter"))

	if m.PermissionMode() == agent.PermissionModeFullAccess {
		t.Fatal("菜单里直接选中 full-access 就生效了，缺少确认")
	}
	if m.state != statePermissionsConfirm {
		t.Fatalf("没有进入确认状态: %v", m.state)
	}
	// 确认才生效
	m = pressKey(m, key("enter"))
	if m.PermissionMode() != agent.PermissionModeFullAccess {
		t.Fatalf("确认后模式为 %q", m.PermissionMode())
	}
}

// TestPermissionsFullAccessConfirmCanBeCancelled 断言确认可以取消。
func TestPermissionsFullAccessConfirmCanBeCancelled(t *testing.T) {
	m := permTestModel(t)
	original := m.PermissionMode()
	m.handleInput("/permissions")
	m = pressKey(m, key("up"))
	m = pressKey(m, key("up"))
	m = pressKey(m, key("enter"))
	if m.state != statePermissionsConfirm {
		t.Fatalf("前提不成立: %v", m.state)
	}
	m = pressKey(m, key("esc"))

	if m.PermissionMode() != original {
		t.Fatalf("取消后模式变成 %q", m.PermissionMode())
	}
	// esc 从确认页退回选择页（用户是中途反悔），再按一次才离开。
	if m.state != statePermissions {
		t.Fatalf("esc 应退回选择页，实际 %v", m.state)
	}
	m = pressKey(m, key("esc"))
	if m.state != stateIdle {
		t.Fatalf("再次 esc 后没有回到 idle: %v", m.state)
	}
}

// TestPermissionsDirectCommandStillWorks 断言带参数的写法仍然可用 ——
// 菜单是补充，不是替代。
func TestPermissionsDirectCommandStillWorks(t *testing.T) {
	m := permTestModel(t)
	m.handleInput("/permissions auto-review")
	if m.PermissionMode() != agent.PermissionModeAutoReview {
		t.Fatal("直接命令失效了")
	}
	if m.state != stateIdle {
		t.Fatalf("直接命令不该打开菜单: %v", m.state)
	}
}

// TestPermissionsMenuShowsCurrentMode 断言菜单标出当前模式，
// 否则用户不知道该往哪边选。
func TestPermissionsMenuShowsCurrentMode(t *testing.T) {
	m := permTestModel(t)
	m.handleInput("/permissions auto-review")
	m.handleInput("/permissions")

	menu := plainText(m.renderPermissionsMenu())
	if !strings.Contains(menu, "替我审核") {
		t.Fatalf("菜单没有列出 auto-review 档:\n%s", menu)
	}
	// 当前项应有光标与当前标记
	if !strings.Contains(menu, "▶") {
		t.Fatalf("菜单没有光标标记:\n%s", menu)
	}
	if !strings.Contains(menu, "*") {
		t.Fatalf("菜单没有标出当前生效的模式:\n%s", menu)
	}
}

// TestPermissionsMenuDoesNotOverflow 断言菜单在窄/矮终端下不溢出。
func TestPermissionsMenuDoesNotOverflow(t *testing.T) {
	for _, w := range []int{120, 80, 60, 40, 24} {
		for _, h := range []int{40, 24, 16, 12, 8} {
			m := permTestModel(t)
			model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
			m = model.(*Model)
			m.handleInput("/permissions")

			view := m.renderFrame()
			if rows := lineCount(view); rows > h {
				t.Fatalf("%dx%d: 帧 %d 行溢出", w, h, rows)
			}
			if opens, closes := strings.Count(view, "╭"), strings.Count(view, "╰"); opens != closes {
				t.Fatalf("%dx%d: 框不闭合 %d/%d", w, h, opens, closes)
			}
		}
	}
}

// TestPermissionsMenuWhileRunningIsRejected 断言运行中不允许切换模式。
func TestPermissionsMenuWhileRunningIsRejected(t *testing.T) {
	m := permTestModel(t)
	m.state = stateRunning
	m.handleInput("/permissions")
	if m.state == statePermissions {
		t.Fatal("运行中仍然打开了权限菜单")
	}
}

// TestPermissionsMenuBoundaries 覆盖边界。
func TestPermissionsMenuBoundaries(t *testing.T) {
	t.Run("重复打开不叠加", func(t *testing.T) {
		m := permTestModel(t)
		m.handleInput("/permissions")
		first := m.permissionsCursor
		m.handleInput("/permissions")
		if m.permissionsCursor != first {
			t.Fatal("重复打开改变了光标")
		}
	})

	t.Run("向上越界不 panic", func(t *testing.T) {
		m := permTestModel(t)
		m.handleInput("/permissions")
		for i := 0; i < 10; i++ {
			m = pressKey(m, key("up"))
		}
		if m.permissionsCursor != 0 {
			t.Fatalf("光标越界到 %d", m.permissionsCursor)
		}
	})

	t.Run("向下越界不 panic", func(t *testing.T) {
		m := permTestModel(t)
		m.handleInput("/permissions")
		for i := 0; i < 10; i++ {
			m = pressKey(m, key("down"))
		}
		if m.permissionsCursor != len(permissionMenuItems)-1 {
			t.Fatalf("光标越界到 %d", m.permissionsCursor)
		}
	})

	t.Run("确认态下 esc 回菜单而非退出", func(t *testing.T) {
		m := permTestModel(t)
		m.handleInput("/permissions")
		m = pressKey(m, key("up"))
		m = pressKey(m, key("up"))
		m = pressKey(m, key("enter"))
		if m.state != statePermissionsConfirm {
			t.Fatal("前提不成立")
		}
		_ = m.renderFrame() // 不 panic
		_ = m.renderPermissionsMenu()
	})
}
