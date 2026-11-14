package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.Arrays;
import org.junit.jupiter.api.Test;

/**
 * The parts of patching that are testable without a live {@link
 * java.lang.instrument.Instrumentation}: how a patch is addressed, and that the digest actually
 * distinguishes two versions of a class. Redefinition itself needs a real attached JVM and is
 * verified by a lab deploy.
 */
final class PatchServiceTest {

  /**
   * A class name is only unique per loader — two webapps routinely load different {@code
   * example.application.…} classes under the same name, and patching one must not be recorded
   * against, or restored into, the other.
   */
  @Test
  void addressesAPatchByLoaderAndClassTogether() {
    assertEquals(
        "18b4aac2|example.application.pas.bll.ActivityBll",
        PatchService.key("18b4aac2", "example.application.pas.bll.ActivityBll"));
    assertNotEquals(
        PatchService.key("18b4aac2", "com.example.Same"),
        PatchService.key("5e7ea81b", "com.example.Same"));
  }

  /**
   * Restore walks keys by the {@code <loader>|<class>} prefix to pick up nested classes, so the
   * separator has to keep a class from matching a differently named sibling that merely shares its
   * prefix.
   */
  @Test
  void keepsANestedClassPrefixFromMatchingAnUnrelatedSibling() {
    String outer = PatchService.key("18b4aac2", "com.example.Activity");

    assertTrue(PatchService.key("18b4aac2", "com.example.Activity$Row").startsWith(outer + "$"));
    assertTrue(
        !PatchService.key("18b4aac2", "com.example.ActivityOther").startsWith(outer + "$"),
        "ActivityOther is a different class, not a nested one");
  }

  /**
   * A lease renewal fires every 40s for as long as a span holds a patch, and it must answer without
   * reading the class back: on the cycle JVM that read is a retransformation that silently reverts
   * the patch it was asked about. So the answer comes from the stash alone — a class is held while
   * its original is stashed — which is a pure function of the stash and the lease table.
   */
  @Test
  void reportsAHeldPatchFromTheStashAlone() {
    java.util.Map<String, byte[]> stash = new java.util.HashMap<>();
    stash.put(
        PatchService.key("2298d741", "example.application.pas.dal.ActivityDal"),
        new byte[] {1, 2, 3});

    java.util.Map<String, Object> held =
        PatchService.heldStatus(
            "2298d741", "example.application.pas.dal.ActivityDal", stash, 1_789_000_000_000L);

    assertEquals(Boolean.TRUE, held.get("patched"));
    assertEquals(1_789_000_000_000L, held.get("leaseExpiresAt"));
    assertEquals("example.application.pas.dal.ActivityDal", held.get("class"));
    assertEquals("2298d741", held.get("loader"));
  }

  /** A class whose original is not stashed was restored, or never patched. */
  @Test
  void reportsAnUnstashedClassAsNotPatched() {
    java.util.Map<String, Object> held =
        PatchService.heldStatus(
            "2298d741", "example.application.pas.dal.ActivityDal", new java.util.HashMap<>(), 0L);

    assertEquals(Boolean.FALSE, held.get("patched"));
  }

  @Test
  void digestDistinguishesTwoVersionsOfTheSameClass() {
    byte[] before = "class-bytes-v1".getBytes();
    byte[] after = "class-bytes-v2".getBytes();

    assertNotEquals(BytecodeSource.digest(before), BytecodeSource.digest(after));
    assertEquals(
        BytecodeSource.digest(before),
        BytecodeSource.digest(Arrays.copyOf(before, before.length)),
        "the same bytes must always digest the same, or every class would look patched");
    assertTrue(BytecodeSource.digest(before).startsWith(BytecodeSource.DIGEST_PREFIX));
  }
}
