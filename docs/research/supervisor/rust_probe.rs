use serde_json::{json, Value};
use std::time::Duration;

#[tokio::main]
async fn main() {
    let mut client = herdr_sdk::Client::connect().expect("connect SDK");
    let ping: Value = client.call("ping", json!({})).expect("SDK ping");
    println!("{}", json!({"probe":"herdr-sdk ping", "result":ping}));
    let panes: herdr_sdk::model::PaneList = client
        .call("pane.list", json!({}))
        .expect("typed SDK pane.list");
    println!(
        "{}",
        json!({"probe":"herdr-sdk typed pane.list", "count":panes.panes.len()})
    );
    let framework = herdr_plugin::HerdrClient::new();
    let listed = tokio::time::timeout(
        Duration::from_secs(5),
        framework.pane().list(Default::default()),
    )
    .await;
    match listed {
        Ok(Ok(_)) => println!(
            "{}",
            json!({"probe":"herdr-plugin typed CLI pane.list", "ok":true})
        ),
        Ok(Err(e)) => println!(
            "{}",
            json!({"probe":"herdr-plugin typed CLI pane.list", "ok":false, "error":e.to_string()})
        ),
        Err(_) => println!(
            "{}",
            json!({"probe":"herdr-plugin typed CLI pane.list", "ok":false, "error":"timeout"})
        ),
    }
}
