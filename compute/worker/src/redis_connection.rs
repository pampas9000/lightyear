use anyhow::Result;
use std::time::Duration;

/// Attempts to establish a multiplexed asynchronous connection to Redis once.
/// Used at startup to fail-fast if Redis is unreachable.
pub async fn connect_once(client: &redis::Client) -> Result<redis::aio::MultiplexedConnection> {
    let conn = client.get_multiplexed_async_connection().await?;
    Ok(conn)
}

/// Establishes a multiplexed asynchronous connection with an exponential backoff retry loop.
/// Capped at a maximum backoff duration of 10 seconds. Used for runtime connection recovery.
pub async fn connect_with_retry(client: &redis::Client) -> redis::aio::MultiplexedConnection {
    let mut backoff = Duration::from_secs(1);
    loop {
        println!("Connecting to Redis...");
        match client.get_multiplexed_async_connection().await {
            Ok(conn) => {
                println!("Connected to Redis successfully.");
                return conn;
            }
            Err(err) => {
                eprintln!("Failed to connect to Redis: {}. Retrying in {:?}...", err, backoff);
                tokio::time::sleep(backoff).await;
                // Exponential backoff capped at 10 seconds
                backoff = std::cmp::min(backoff * 2, Duration::from_secs(10));
            }
        }
    }
}
