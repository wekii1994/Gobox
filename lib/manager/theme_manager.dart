import 'dart:math';

import 'package:gobox/common/common.dart';
import 'package:gobox/common/theme.dart';
import 'package:gobox/providers/config.dart';
import 'package:gobox/state.dart';
import 'package:material_ui/material_ui.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/state.dart';

class ThemeManager extends ConsumerStatefulWidget {
  final Widget child;

  const ThemeManager({super.key, required this.child});

  @override
  ConsumerState<ThemeManager> createState() => _ThemeManagerState();
}

class _ThemeManagerState extends ConsumerState<ThemeManager> {
  ThemeData? _theme;
  double? _textScaleFactor;

  void _syncThemeMetrics(double textScaleFactor) {
    final theme = Theme.of(context);
    if (identical(theme, _theme) && textScaleFactor == _textScaleFactor) {
      return;
    }
    _theme = theme;
    _textScaleFactor = textScaleFactor;
    globalState.measure = Measure.of(context, textScaleFactor);
    globalState.theme = CommonTheme.of(context, textScaleFactor);
  }

  Widget _buildSystemUi(Widget child) {
    if (!system.isAndroid) {
      return child;
    }
    return Consumer(
      builder: (context, ref, _) {
        final brightness = ref.watch(currentBrightnessProvider);
        final iconBrightness = brightness == Brightness.light
            ? Brightness.dark
            : Brightness.light;
        final style = SystemUiOverlayStyle(
          statusBarColor: Colors.transparent,
          statusBarIconBrightness: iconBrightness,
          systemStatusBarContrastEnforced: false,
          systemNavigationBarIconBrightness: iconBrightness,
          systemNavigationBarColor: Colors.transparent,
          systemNavigationBarDividerColor: Colors.transparent,
          systemNavigationBarContrastEnforced: false,
        );
        if (globalState.appState.systemUiOverlayStyle != style) {
          globalState.appState = globalState.appState.copyWith(
            systemUiOverlayStyle: style,
          );
        }
        return AnnotatedRegion<SystemUiOverlayStyle>(
          value: style,
          sized: false,
          child: child,
        );
      },
    );
  }

  // _buildScrollbar(Widget child) {
  //   return Consumer(
  //     builder: (_, ref, child) {
  //       final isMobileView = ref.read(isMobileViewProvider);
  //       if (isMobileView) {
  //         return ScrollConfiguration(
  //           behavior: HiddenBarScrollBehavior(),
  //           child: child!,
  //         );
  //       }
  //       return child!;
  //     },
  //     child: child,
  //   );
  // }

  @override
  Widget build(BuildContext context) {
    final textScale = ref.read(
      themeSettingProvider.select((state) => state.textScale),
    );
    final pageScale = ref.watch(
      themeSettingProvider.select((state) => state.pageScale),
    );
    final effectivePageScale = pageScale.isFinite
        ? pageScale.clamp(minPageScale, maxPageScale).toDouble()
        : defaultPageScale;

    final double textScaleFactor = max(
      min(
        textScale.enable ? textScale.scale : defaultTextScaleFactor,
        maxTextScale,
      ),
      minTextScale,
    );

    _syncThemeMetrics(textScaleFactor);
    final mediaQuery = MediaQuery.of(context);
    final padding = mediaQuery.padding;
    final height = mediaQuery.size.height;
    final baseData = mediaQuery.copyWith(
      textScaler: TextScaler.linear(textScaleFactor),
      padding: padding.copyWith(
        top: padding.top > height * 0.3 ? 20.0 : padding.top,
      ),
    );

    final inner = LayoutBuilder(
      builder: (_, container) {
        globalState.appController.updateViewSize(
          Size(container.maxWidth, container.maxHeight),
        );
        return _buildSystemUi(widget.child);
      },
    );

    if (effectivePageScale == 1.0) {
      return MediaQuery(
        data: baseData,
        child: inner,
      );
    }

    final originalSize = baseData.size;
    final scaledSize = originalSize / effectivePageScale;
    return MediaQuery(
      data: baseData.copyWith(
        size: scaledSize,
        padding: baseData.padding / effectivePageScale,
        viewPadding: baseData.viewPadding / effectivePageScale,
        viewInsets: baseData.viewInsets / effectivePageScale,
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
            scale: effectivePageScale,
            alignment: Alignment.topLeft,
            child: inner,
          ),
        ),
      ),
    );
  }
}
