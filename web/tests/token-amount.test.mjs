import { test } from "vitest";
import assert from "node:assert/strict";
import { formatTokenAmount } from "../src/components/agent/tokenAmount";

test("token amounts stay exact below 10k", () => {
  assert.equal(formatTokenAmount(0), "0");
  assert.equal(formatTokenAmount(999), "999");
  assert.equal(formatTokenAmount(6630), "6,630");
  assert.equal(formatTokenAmount(9999), "9,999");
});

test("token amounts use k from 10k up, without false precision", () => {
  assert.equal(formatTokenAmount(10000), "10k");
  assert.equal(formatTokenAmount(12345), "12.3k");
  assert.equal(formatTokenAmount(6129539), "6130k");
  assert.equal(formatTokenAmount(-1), "0");
  assert.equal(formatTokenAmount(Number.NaN), "0");
});
