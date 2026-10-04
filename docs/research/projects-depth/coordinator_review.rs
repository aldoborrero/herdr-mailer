//! Independent characterization probes for the adoption study.
//! Passing means the named behavior was reproduced, not that it is desirable.
use std::cell::Cell;
use std::path::Path;
use std::rc::Rc;

use crate::herdr::{Agent, AgentSession};
use crate::runner::fake::{ok, timeout, FakeRunner};
use crate::scenarios::{agent_json, World};
use crate::{remote, steps, thread, threads, ticker};

#[test]
fn review_capacity_is_advisory_and_identical_starts_allocate_distinct_threads() {
    let w = World::new();
    let p = w.project("demo", "a.sock");
    crate::settings::set(&w.ctx(), "demo", "max_parallel_threads", "1").unwrap();
    let repo = w.home.path().join("repo");
    std::fs::create_dir(&repo).unwrap();
    w.runner.on("rev-parse --show-toplevel", ok("/repo"));
    w.runner.on("remote get-url", ok(""));
    w.runner.on("symbolic-ref", ok("main"));
    w.runner.on("rev-parse --git-path", crate::runner::fake::fail(1, "not git"));
    let n = Rc::new(Cell::new(0));
    let n2 = n.clone();
    let root = w.home.path().to_path_buf();
    w.runner.on_fn(|c| c.display().contains("worktree create"), move |_| {
        let i = n2.get() + 1;
        n2.set(i);
        let wt = root.join(format!("wt{i}"));
        std::fs::create_dir(&wt).unwrap();
        Ok(ok(&format!(r#"{{"result":{{"root_pane":{{"workspace_id":"w{i}","tab_id":"w{i}:t1","pane_id":"w{i}:p1","cwd":"{}"}},"worktree":{{"path":"{}"}}}}}}"#, wt.display(), wt.display())))
    });
    let args = || threads::StartArgs {
        title: "Same task".into(), repo: Some(repo.to_string_lossy().into_owned()),
        machine: None, profile: None, kind: None, base: Some("main".into()), task: "Same request".into(),
    };
    let a = threads::start(&w.ctx(), "demo", args()).unwrap();
    let b = threads::start(&w.ctx(), "demo", args()).unwrap();
    assert_ne!(a.id, b.id);
    assert_eq!(thread::list(&p).len(), 2);
    assert_eq!(n.get(), 2);
}

#[test]
fn review_replacing_native_session_does_not_change_thread_match() {
    let w = World::new();
    let p = w.project("demo", "a.sock");
    let t = w.thread(&p, w.home.path(), |_| {});
    let mut a: Agent = serde_json::from_str(&agent_json("w2", "w2:t1", "w2:p1", &t.cwd, &t.agent_name, "idle")).unwrap();
    a.agent_session = Some(AgentSession { value: "old-session".into() });
    assert!(thread::agent_matches(&t, &a));
    a.agent_session = Some(AgentSession { value: "replacement-session".into() });
    a.terminal_id = "replacement-terminal".into();
    assert!(thread::agent_matches(&t, &a));
}

#[test]
fn review_remote_branch_probe_timeout_is_reported_as_absent() {
    let runner = FakeRunner::new();
    runner.on("ssh", timeout());
    assert!(!remote::branch_exists(&runner, "worker", "/repo", "hp/demo/t-0001").unwrap());
}

#[test]
fn review_followup_can_be_delivered_before_its_durable_record_fails() {
    let w = World::new();
    let p = w.project("demo", "a.sock");
    let t = w.thread(&p, w.home.path(), |_| {});
    *w.agents.borrow_mut() = format!("[{}]", agent_json("w2", "w2:t1", "w2:p1", &t.cwd, &t.agent_name, "idle"));
    w.runner.on("agent prompt", ok(r#"{"result":{}}"#));
    // Make rename of the task record fail, after the runtime call succeeds.
    std::fs::create_dir(thread::task_path(&p, &t.id)).unwrap();
    assert!(threads::prompt(&w.ctx(), "demo", &t.id, "A follow-up that must survive").is_err());
    assert_eq!(w.runner.count("agent prompt"), 1);
    assert!(thread::task_path(&p, &t.id).is_dir());
}

#[test]
fn review_first_project_monopolizes_polling_of_a_shared_remote_machine() {
    let base = World::new();
    let a = base.project("alpha", "a.sock");
    let b = base.project("beta", "a.sock");
    base.thread(&a, Path::new("/remote/alpha"), |t| t.machine = "box".into());
    base.thread(&b, Path::new("/remote/beta"), |t| {
        t.machine = "box".into(); t.prompt_pending = true;
        t.workspace_id = "w3".into(); t.tab_id = "w3:t1".into(); t.pane_id = "w3:p1".into();
    });
    let w = World { runner: FakeRunner::new(), ..base };
    w.runner.on_fn(|c| c.args.first().is_some_and(|a| a == "--machine") && c.display().contains("agent list"), |_| Ok(ok(r#"{"result":{"agents":[{"pane_id":"w2:p1","tab_id":"w2:t1","workspace_id":"w2","cwd":"/remote/alpha","agent":"claude","agent_status":"working","name":"hp-alpha-t-0001"}]}}"#)));
    w.runner.on_fn(|c| c.args.first().is_some_and(|a| a == "--machine") && c.display().contains("pane list"), |_| Ok(ok(r#"{"result":{"panes":[{"pane_id":"w2:p1","tab_id":"w2:t1","workspace_id":"w2","cwd":"/remote/alpha"},{"pane_id":"w3:p1","tab_id":"w3:t1","workspace_id":"w3","cwd":"/remote/beta"}]}}"#)));
    w.runner.on("agent list", ok(r#"{"result":{"agents":[]}}"#));
    w.runner.on("pane list", ok(r#"{"result":{"panes":[]}}"#));
    w.runner.on("machine list --json", ok(r#"[{"id":"1","label":"box","target":"worker"}]"#));
    w.runner.on("ssh", ok(""));
    w.runner.on("agent start", ok(r#"{"result":{"agent":{"pane_id":"w2:p1","tab_id":"w2:t1","workspace_id":"w2"}}}"#));
    w.runner.on("report-metadata", ok("{}"));
    let ctx = w.ctx();
    let mut memory = steps::Memory::new(&ctx);
    for _ in 0..65 {
        assert!(ticker::tick_for_test(&ctx, &mut memory));
    }
    assert_eq!(thread::load(&a, "t-0001").unwrap().last_state, "working");
    assert_eq!(thread::load(&b, "t-0001").unwrap().launch_attempts, 0);
    let calls = w.runner.calls.borrow();
    let ssh_calls: Vec<_> = calls.iter().filter(|c| c.program == "ssh").collect();
    assert_eq!(ssh_calls.len(), 17);
    assert!(ssh_calls.iter().all(|c| c.display().contains("/remote/alpha")));
    assert!(ssh_calls.iter().all(|c| !c.display().contains("/remote/beta")));
}

#[test]
fn review_corrupt_thread_is_omitted_from_inventory() {
    let w = World::new();
    let p = w.project("demo", "a.sock");
    let t = w.thread(&p, w.home.path(), |_| {});
    assert_eq!(thread::list(&p).len(), 1);
    std::fs::write(thread::record_path(&p, &t.id), "broken = [").unwrap();
    assert!(thread::load(&p, &t.id).is_err());
    assert!(thread::list(&p).is_empty());
}

#[test]
fn review_restart_refuses_an_ambiguous_preexisting_branch() {
    let t = thread::Thread { id: "t-0001".into(), kind: thread::Kind::Worktree, status: thread::Status::Failed, ..Default::default() };
    let error = threads::restart_plan(&t, &thread::Live::default(), true, jiff::Timestamp::now()).unwrap_err();
    assert!(error.to_string().contains("half-made worktree"));
}
