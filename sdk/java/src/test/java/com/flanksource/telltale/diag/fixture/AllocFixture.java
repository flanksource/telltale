package com.flanksource.telltale.diag.fixture;

/**
 * A class whose methods allocate a known amount, for asserting what a probe armed with {@code
 * alloc} measures. Each array is returned rather than dropped, so escape analysis cannot remove the
 * allocation the test is counting.
 */
public class AllocFixture {

  /** Allocates {@code bytes} bytes of array payload and hands the array back. */
  public byte[] fill(int bytes) {
    return new byte[bytes];
  }

  /** Calls {@link #fill}, so a call-site frame and its caller both carry the allocation. */
  public int outer(int bytes) {
    return fill(bytes).length;
  }
}
