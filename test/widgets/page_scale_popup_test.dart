import 'package:flutter_test/flutter_test.dart';
import 'package:gobox/models/models.dart';
import 'package:gobox/widgets/popup.dart';
import 'package:material_ui/material_ui.dart';

Widget _scaledHost({required double scale, required Widget child}) {
  return MaterialApp(
    builder: (context, navChild) {
      final mediaQuery = MediaQuery.of(context);
      final originalSize = mediaQuery.size;
      final scaledSize = originalSize / scale;
      return MediaQuery(
        data: mediaQuery.copyWith(
          size: scaledSize,
          padding: mediaQuery.padding / scale,
          viewPadding: mediaQuery.viewPadding / scale,
          viewInsets: mediaQuery.viewInsets / scale,
        ),
        child: SizedBox(
          width: originalSize.width,
          height: originalSize.height,
          child: OverflowBox(
            minWidth: scaledSize.width,
            maxWidth: scaledSize.width,
            minHeight: scaledSize.height,
            maxHeight: scaledSize.height,
            alignment: Alignment.topLeft,
            child: Transform.scale(
              scale: scale,
              alignment: Alignment.topLeft,
              child: navChild,
            ),
          ),
        ),
      );
    },
    home: Scaffold(body: child),
  );
}

void main() {
  testWidgets('CommonPopupBox stays aligned under non-1.0 pageScale', (
    tester,
  ) async {
    const scale = 0.7;
    tester.view.physicalSize = const Size(800, 600);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    final buttonKey = GlobalKey();
    await tester.pumpWidget(
      _scaledHost(
        scale: scale,
        child: Align(
          alignment: Alignment.topRight,
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: CommonPopupBox(
              targetBuilder: (open) => IconButton(
                key: buttonKey,
                onPressed: open,
                icon: const Icon(Icons.more_vert),
              ),
              popup: CommonPopupMenu(
                items: [
                  PopupMenuItemData(label: 'scaled-action', onPressed: () {}),
                ],
              ),
            ),
          ),
        ),
      ),
    );

    final buttonRect = tester.getRect(find.byKey(buttonKey));
    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();

    final menuRect = tester.getRect(find.byType(CommonPopupMenu));
    expect((menuRect.right - buttonRect.right).abs(), lessThan(80));
    expect(menuRect.top, closeTo(buttonRect.top, 40));
  });

  testWidgets(
    'showContextMenu converts globalPosition under non-1.0 pageScale',
    (tester) async {
      const scale = 0.7;
      tester.view.physicalSize = const Size(800, 600);
      tester.view.devicePixelRatio = 1.0;
      addTearDown(() {
        tester.view.resetPhysicalSize();
        tester.view.resetDevicePixelRatio();
      });

      const tapScreenPos = Offset(700, 120);
      await tester.pumpWidget(
        _scaledHost(
          scale: scale,
          child: Builder(
            builder: (context) => Center(
              child: GestureDetector(
                onTap: () => showContextMenu(
                  context,
                  position: tapScreenPos,
                  items: [
                    PopupMenuItemData(label: 'ctx-action', onPressed: () {}),
                  ],
                ),
                child: const Text('trigger'),
              ),
            ),
          ),
        ),
      );

      await tester.tap(find.text('trigger'));
      await tester.pumpAndSettle();

      final menuRect = tester.getRect(find.byType(CommonPopupMenu));
      expect(
        menuRect.left <= tapScreenPos.dx + 2 &&
            menuRect.right >= tapScreenPos.dx - 2,
        isTrue,
      );
      expect(menuRect.top, closeTo(tapScreenPos.dy, 15));
    },
  );
}
