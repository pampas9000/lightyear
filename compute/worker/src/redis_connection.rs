use anyhow::Result;
use redis::AsyncConnectionConfig;
use std::time::Duration;

/// Setup budget for establishing a new connection.
///
/// The `redis` crate defaults this to **one second**, and it covers DNS
/// resolution, the TCP handshake and the TLS handshake put together. Against a
/// remote managed Redis (Upstash and friends) that is routinely not enough, and
/// the failure surfaces as a bare "timed out" at startup — while every other
/// client in the stack, which has no such aggressive default, connects fine.
const CONNECTION_SETUP_TIMEOUT: Duration = Duration::from_secs(15);

/// Connection parameters shared by the startup probe and the runtime reconnects.
fn connection_config() -> AsyncConnectionConfig {
    AsyncConnectionConfig::new().set_connection_timeout(Some(CONNECTION_SETUP_TIMEOUT))
}

/// Attempts to establish a multiplexed asynchronous connection to Redis once.
/// Used at startup to fail-fast if Redis is unreachable.
pub async fn connect_once(client: &redis::Client) -> Result<redis::aio::MultiplexedConnection> {
    let conn = client
        .get_multiplexed_async_connection_with_config(&connection_config())
        .await?;
    Ok(conn)
}

/// Establishes a multiplexed asynchronous connection with an exponential backoff retry loop.
/// Capped at a maximum backoff duration of 10 seconds. Used for runtime connection recovery.
pub async fn connect_with_retry(client: &redis::Client) -> redis::aio::MultiplexedConnection {
    let mut backoff = Duration::from_secs(1);
    loop {
        println!("Connecting to Redis...");
        match client
            .get_multiplexed_async_connection_with_config(&connection_config())
            .await
        {
            Ok(conn) => {
                println!("Connected to Redis successfully.");
                return conn;
            }
            Err(err) => {
                eprintln!(
                    "Failed to connect to Redis: {}. Retrying in {:?}...",
                    err, backoff
                );
                tokio::time::sleep(backoff).await;
                // Exponential backoff capped at 10 seconds
                backoff = std::cmp::min(backoff * 2, Duration::from_secs(10));
            }
        }
    }
}
