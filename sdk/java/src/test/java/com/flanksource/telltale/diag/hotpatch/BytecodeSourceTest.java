package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;

import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

/**
 * The digest is the §3 cache key and the patch-drift detector, so it has to be a pure, stable
 * function of the class bytes.
 */
final class BytecodeSourceTest {

  @ParameterizedTest(name = "sha256({0}) = {1}")
  @CsvSource({
    // Independently computed with `printf '%s' <input> | shasum -a 256`.
    "'', e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "telltale-diag, 917d29de0a6609a91a96aa9c9051862a26347cb1e4df8e1870801f2db9237eda",
  })
  void digestsBytesAsAPrefixedLowerCaseSha256(String input, String expected) {
    assertEquals(
        BytecodeSource.DIGEST_PREFIX + expected,
        BytecodeSource.digest(input.getBytes(StandardCharsets.UTF_8)));
  }

  @Test
  void isStableAcrossCallsAndAcrossEqualButDistinctArrays() {
    byte[] bytes = "example.application.pas.dal.ActivityDal".getBytes(StandardCharsets.UTF_8);
    byte[] copy = Arrays.copyOf(bytes, bytes.length);

    assertEquals(BytecodeSource.digest(bytes), BytecodeSource.digest(bytes));
    assertEquals(BytecodeSource.digest(bytes), BytecodeSource.digest(copy));
  }

  @Test
  void changesWhenASingleByteOfTheClassFileChanges() {
    byte[] before = new byte[] {(byte) 0xCA, (byte) 0xFE, (byte) 0xBA, (byte) 0xBE, 0, 0, 0, 52};
    byte[] after = Arrays.copyOf(before, before.length);
    after[after.length - 1] = 51;

    assertNotEquals(BytecodeSource.digest(before), BytecodeSource.digest(after));
  }
}
